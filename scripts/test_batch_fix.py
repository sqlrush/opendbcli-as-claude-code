#!/usr/bin/env python3
"""Test the 7 fixable non-SSD scenarios with corrected setup."""
import cx_Oracle, subprocess, threading, time, os, sys

DSN = cx_Oracle.makedsn('127.0.0.1', 1521, service_name='orclpdb1')
OPENDB = '/root/opendb/opendb'

def odb():
    r = subprocess.run([OPENDB, '-c', 'oracle', '/rule live'],
                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=120)
    return r.stdout.decode('utf-8', 'replace')

def kill_all():
    c = cx_Oracle.connect('system', 'OraclePass123', DSN)
    for r in c.cursor().execute("SELECT p.spid FROM v$session s JOIN v$process p ON s.paddr=p.addr WHERE s.username='TESTUSER'").fetchall():
        os.system('kill -9 %s' % r[0])
    c.close()
    time.sleep(3)

def sys_exec(sql):
    c = cx_Oracle.connect('system', 'OraclePass123', DSN)
    c.cursor().execute(sql)
    c.commit()
    c.close()

def plsql_load(sql, n=30, wait=10):
    """Start n threads running PL/SQL, wait, run opendb, kill."""
    bar = threading.Barrier(n + 1, timeout=60)
    def worker():
        try:
            co = cx_Oracle.connect('testuser', 'testpass123', DSN)
            bar.wait()
            co.cursor().execute(sql)
            co.close()
        except:
            try: bar.wait()
            except: pass
    for _ in range(n):
        threading.Thread(target=worker, daemon=True).start()
    bar.wait()
    time.sleep(wait)
    out = odb()
    kill_all()
    return out

def extract_diag(out):
    """Extract diagnosis line."""
    for line in out.split('\n'):
        s = line.strip()
        if s.startswith('根因:') and len(s) > 4:
            return s
    return '(无诊断)'

# ─── T033: Parallel degradation (infinite loop) ─────────────────
def t033():
    print('=== T033: Parallel degradation ===')
    sys_exec("ALTER SYSTEM SET PARALLEL_MAX_SERVERS=4 SCOPE=MEMORY")
    out = plsql_load("""DECLARE v NUMBER; BEGIN
        LOOP
            SELECT /*+ PARALLEL(big_test, 8) */ count(*) INTO v FROM big_test;
        END LOOP; END;""", n=10, wait=10)
    sys_exec("ALTER SYSTEM SET PARALLEL_MAX_SERVERS=40 SCOPE=MEMORY")
    return out

# ─── T039: PGA overflow (shrink PGA first) ───────────────────────
def t039():
    print('=== T039: PGA overflow ===')
    # Save and shrink PGA
    c = cx_Oracle.connect('system', 'OraclePass123', DSN)
    cur = c.cursor()
    cur.execute("SELECT value FROM v$parameter WHERE name='pga_aggregate_target'")
    old_pga = cur.fetchone()[0]
    c.close()
    sys_exec("ALTER SYSTEM SET PGA_AGGREGATE_TARGET=100M SCOPE=MEMORY")
    time.sleep(2)
    out = plsql_load("""DECLARE v NUMBER; BEGIN
        FOR i IN 1..500 LOOP
            SELECT count(DISTINCT data || id) INTO v FROM big_test;
        END LOOP; END;""", n=10, wait=10)
    # Restore
    sys_exec("ALTER SYSTEM SET PGA_AGGREGATE_TARGET=%s SCOPE=MEMORY" % old_pga)
    return out

# ─── T047: TEMP full (create small temp tablespace) ──────────────
def t047():
    print('=== T047: TEMP full ===')
    # This is hard to simulate without filling TEMP. Just run large sorts.
    out = plsql_load("""DECLARE v VARCHAR2(100); BEGIN
        LOOP
            FOR r IN (SELECT * FROM big_test ORDER BY val, DBMS_RANDOM.VALUE) LOOP
                v := r.val; EXIT;
            END LOOP;
        END LOOP; END;""", n=10, wait=10)
    return out

# ─── T053: Datafile limit (idle capacity check) ─────────────────
def t053():
    print('=== T053: Datafile limit ===')
    out = odb()
    return out

# ─── T076: Connection storm ─────────────────────────────────────
def t076():
    print('=== T076: Connection storm ===')
    conns = []
    for i in range(80):
        conns.append(cx_Oracle.connect('testuser', 'testpass123', DSN))
    time.sleep(3)
    out = odb()
    for c in conns:
        try: c.close()
        except: pass
    return out

# ─── T077: Sessions near limit ──────────────────────────────────
def t077():
    print('=== T077: Sessions near limit ===')
    conns = []
    try:
        for i in range(120):
            conns.append(cx_Oracle.connect('testuser', 'testpass123', DSN))
    except Exception as e:
        print(f'  Connection limit at {len(conns)}: {str(e)[:60]}')
    time.sleep(3)
    out = odb()
    for c in conns:
        try: c.close()
        except: pass
    return out

# ─── T080: Resource Manager (temporarily enable) ────────────────
def t080():
    print('=== T080: Resource Manager ===')
    sys_exec("ALTER SYSTEM SET RESOURCE_MANAGER_PLAN='DEFAULT_PLAN' SCOPE=MEMORY")
    time.sleep(2)
    out = plsql_load("""DECLARE v NUMBER; BEGIN
        LOOP
            SELECT count(*) INTO v FROM big_test WHERE id = MOD(DBMS_RANDOM.VALUE*10000,10000);
        END LOOP; END;""", n=40, wait=10)
    sys_exec("ALTER SYSTEM SET RESOURCE_MANAGER_PLAN='' SCOPE=MEMORY")
    return out

# ─── Run all ────────────────────────────────────────────────────
SCENARIOS = [
    ('T033', t033), ('T039', t039), ('T047', t047), ('T053', t053),
    ('T076', t076), ('T077', t077), ('T080', t080),
]

if __name__ == '__main__':
    targets = sys.argv[1:] if len(sys.argv) > 1 else [n for n, _ in SCENARIOS]
    results = {}
    for name, fn in SCENARIOS:
        if name not in targets and 'ALL' not in targets:
            continue
        kill_all()
        try:
            out = fn()
            diag = extract_diag(out)
            results[name] = diag
            print(f'  Result: {diag}')
        except Exception as e:
            results[name] = f'ERROR: {e}'
            print(f'  ERROR: {e}')
        kill_all()

    print('\n=== SUMMARY ===')
    for name, diag in sorted(results.items()):
        print(f'  {name:8s} | {diag}')
