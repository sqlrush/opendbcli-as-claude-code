#!/usr/bin/env python3
"""Opus evaluation: capture full monitoring data + rule diagnosis for each scenario."""
import cx_Oracle, subprocess, threading, time, os, sys

DSN = cx_Oracle.makedsn('127.0.0.1', 1521, service_name='orclpdb1')
OPENDB = '/root/opendb/opendb'

def kill_all():
    c = cx_Oracle.connect('system', 'OraclePass123', DSN)
    for r in c.cursor().execute("SELECT p.spid FROM v$session s JOIN v$process p ON s.paddr=p.addr WHERE s.username='TESTUSER'").fetchall():
        os.system('kill -9 %s' % r[0])
    c.close()
    time.sleep(3)

def clean_tables():
    c = cx_Oracle.connect('system', 'OraclePass123', DSN)
    cur = c.cursor()
    for t in ['hot_insert_test', 'hw_test', 'commit_test']:
        try: cur.execute('TRUNCATE TABLE testuser.%s' % t)
        except: pass
    for s in ['seq_nocache', 'seq_small_cache', 'seq_hw']:
        try: cur.execute('DROP SEQUENCE testuser.%s' % s)
        except: pass
    c.close()

def run_scenario(name, setup_fn, load_sql, n_threads=30, wait_sec=10):
    """Generic scenario runner: setup → load → capture → diagnose → cleanup."""
    kill_all()
    clean_tables()

    # Setup
    if setup_fn:
        setup_fn()

    # Start load
    bar = threading.Barrier(n_threads + 1, timeout=60)
    def worker():
        try:
            co = cx_Oracle.connect('testuser', 'testpass123', DSN)
            bar.wait()
            co.cursor().execute(load_sql)
            co.close()
        except:
            try: bar.wait()
            except: pass
    for _ in range(n_threads):
        threading.Thread(target=worker, daemon=True).start()
    bar.wait()
    time.sleep(wait_sec)

    # Verify active
    c = cx_Oracle.connect('system', 'OraclePass123', DSN)
    cur = c.cursor()
    cur.execute("SELECT COUNT(*) FROM v$session WHERE username='TESTUSER' AND status='ACTIVE'")
    active = cur.fetchone()[0]

    # Capture monitoring data for Opus evaluation
    cur.execute("""SELECT NVL(event,'ON CPU') event, NVL(wait_class,'CPU') wc,
           COUNT(*) cnt, ROUND(COUNT(*)*100.0/NULLIF(SUM(COUNT(*)) OVER(),0),1) pct
    FROM v$session WHERE status='ACTIVE' AND username IS NOT NULL AND NVL(wait_class,'CPU')!='Idle'
    GROUP BY event, wait_class ORDER BY COUNT(*) DESC FETCH FIRST 8 ROWS ONLY""")
    waits = cur.fetchall()

    cur.execute("""SELECT s.sql_id, COUNT(*) concurrent,
           MAX(CASE WHEN s.wait_class!='Idle' THEN s.event END) event
    FROM v$session s WHERE s.status='ACTIVE' AND s.username IS NOT NULL AND s.sql_id IS NOT NULL
    GROUP BY s.sql_id ORDER BY COUNT(*) DESC FETCH FIRST 3 ROWS ONLY""")
    top_sqls = cur.fetchall()

    # Get plan + stats for top SQL
    sql_details = []
    for row in top_sqls:
        sid = row[0]
        detail = {'sql_id': sid, 'concurrent': row[1], 'event': row[2]}
        try:
            cur.execute("""SELECT MAX(CASE WHEN operation='TABLE ACCESS' AND options='FULL' THEN 1 ELSE 0 END),
                   MAX(CASE WHEN INSTR(operation,'SORT')>0 THEN 1 ELSE 0 END),
                   MAX(CASE WHEN INSTR(operation,'INDEX')>0 THEN 1 ELSE 0 END)
            FROM v$sql_plan WHERE sql_id=:1""", [sid])
            plan = cur.fetchone()
            if plan:
                detail['full_scan'] = plan[0]
                detail['has_sort'] = plan[1]
                detail['has_index'] = plan[2]
        except: pass
        try:
            cur.execute("""SELECT SUM(buffer_gets), SUM(executions), SUM(rows_processed), SUM(disk_reads)
            FROM v$sql WHERE sql_id=:1""", [sid])
            stats = cur.fetchone()
            if stats and stats[1]:
                detail['buf_per_exec'] = int(stats[0] / max(stats[1], 1))
                detail['rows_per_exec'] = int(stats[2] / max(stats[1], 1))
                detail['disk_per_exec'] = int(stats[3] / max(stats[1], 1))
        except: pass
        sql_details.append(detail)

    # Blocking chains
    cur.execute("""SELECT b.blocking_session, MAX(s.username), MAX(s.event), MAX(s.status), COUNT(*)
    FROM v$session b LEFT JOIN v$session s ON s.sid=b.blocking_session
    WHERE b.blocking_session IS NOT NULL
    GROUP BY b.blocking_session ORDER BY COUNT(*) DESC FETCH FIRST 5 ROWS ONLY""")
    blocks = cur.fetchall()
    c.close()

    # Run opendb
    r = subprocess.run([OPENDB, '-c', 'oracle', '/rule live'],
                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=120)
    rule_output = r.stdout.decode('utf-8', 'replace')

    # Extract diagnosis
    diag_section = []
    in_diag = False
    for line in rule_output.split('\n'):
        if '规则诊断' in line:
            in_diag = True
        if in_diag:
            diag_section.append(line)

    # Print structured output for Opus evaluation
    print(f'\n{"="*70}')
    print(f'SCENARIO: {name}')
    print(f'{"="*70}')
    print(f'\n--- 现场数据 ---')
    print(f'活跃会话: {active}')
    print(f'\n等待事件:')
    for w in waits:
        print(f'  {w[0]:40s} {w[1]:15s} {w[2]:>3d} ({w[3]}%)')
    print(f'\nTop SQL:')
    for d in sql_details:
        print(f'  {d["sql_id"]:15s} concurrent={d.get("concurrent",0):>3d} event={d.get("event","")}')
        if 'full_scan' in d:
            print(f'    plan: full_scan={d["full_scan"]} sort={d["has_sort"]} index={d["has_index"]}')
        if 'buf_per_exec' in d:
            print(f'    stats: buf/exec={d["buf_per_exec"]} rows/exec={d["rows_per_exec"]} disk/exec={d["disk_per_exec"]}')
    if blocks:
        print(f'\n阻塞链:')
        for b in blocks:
            print(f'  SID={b[0]} user={b[1]} event={b[2]} status={b[3]} victims={b[4]}')

    print(f'\n--- Rule 诊断 ---')
    for line in diag_section:
        print(line)

    print(f'\n{"="*70}\n')

    kill_all()
    return active

# ─── Scenario definitions ───────────────────────────────────────

def t009():
    def setup():
        c = cx_Oracle.connect('system', 'OraclePass123', DSN)
        try: c.cursor().execute("DROP SEQUENCE testuser.seq_nocache")
        except: pass
        c.cursor().execute("CREATE SEQUENCE testuser.seq_nocache NOCACHE")
        c.commit(); c.close()
        clean_tables()
    return run_scenario('T009-Sequence-NOCACHE', setup,
        """BEGIN FOR i IN 1..100000 LOOP
            INSERT INTO hot_insert_test(id,val) VALUES(seq_nocache.NEXTVAL,'x');
            IF MOD(i,100)=0 THEN COMMIT; END IF;
        END LOOP; COMMIT; END;""", n_threads=30)

def t021():
    return run_scenario('T021-HardParse-SP-OK', None,
        """DECLARE v NUMBER; BEGIN
            FOR i IN 1..30000 LOOP
                EXECUTE IMMEDIATE 'SELECT count(*) FROM big_test WHERE id = '||i INTO v;
            END LOOP; END;""", n_threads=30)

def t040():
    return run_scenario('T040-LibCache-Drop', None,
        """DECLARE v NUMBER; BEGIN
            FOR i IN 1..50000 LOOP
                EXECUTE IMMEDIATE 'SELECT count(*) FROM big_test WHERE id = '||i||' AND ROWNUM=1' INTO v;
            END LOOP; END;""", n_threads=40)

def t042():
    return run_scenario('T042-SP-Fragmentation', None,
        """DECLARE v NUMBER; BEGIN
            FOR i IN 1..30000 LOOP
                EXECUTE IMMEDIATE 'SELECT /* '||RPAD('x',MOD(i,200)+10,'y')||' */ count(*) FROM big_test WHERE id='||i INTO v;
            END LOOP; END;""", n_threads=30)

def t099():
    return run_scenario('T099-Latch-SP', None,
        """DECLARE v NUMBER; BEGIN
            FOR i IN 1..50000 LOOP
                EXECUTE IMMEDIATE 'SELECT count(*) FROM big_test WHERE id = '||i INTO v;
            END LOOP; END;""", n_threads=50)

def t026():
    def setup():
        c = cx_Oracle.connect('testuser', 'testpass123', DSN)
        try:
            c.cursor().execute("""CREATE TABLE skew_test AS
                SELECT ROWNUM id, CASE WHEN ROWNUM<=990000 THEN 'COMMON' ELSE 'RARE' END status,
                RPAD('X',100) data FROM dual CONNECT BY LEVEL<=1000000""")
            c.cursor().execute("CREATE INDEX idx_skew_status ON skew_test(status)")
            c.commit()
        except: pass
        c.close()
    return run_scenario('T026-DataSkew', setup,
        """DECLARE v NUMBER; BEGIN
            FOR i IN 1..20000 LOOP
                IF MOD(i,2)=0 THEN SELECT count(*) INTO v FROM skew_test WHERE status='COMMON';
                ELSE SELECT count(*) INTO v FROM skew_test WHERE status='RARE'; END IF;
            END LOOP; END;""", n_threads=30)

def t058():
    def setup():
        clean_tables()
    return run_scenario('T058-Checkpoint', setup,
        """BEGIN FOR i IN 1..50000 LOOP
            INSERT INTO hw_test(id,data) VALUES(i, RPAD('X',500));
            IF MOD(i,100)=0 THEN COMMIT; END IF;
        END LOOP; COMMIT; END;""", n_threads=20)

def t088():
    def setup():
        clean_tables()
    return run_scenario('T088-LogBuffer', setup,
        """BEGIN FOR i IN 1..30000 LOOP
            INSERT INTO hw_test(id,data) VALUES(i, RPAD('X',1000));
            IF MOD(i,50)=0 THEN COMMIT; END IF;
        END LOOP; COMMIT; END;""", n_threads=20)

SCENARIOS = [
    ('T009', t009), ('T021', t021), ('T026', t026),
    ('T040', t040), ('T042', t042), ('T058', t058),
    ('T088', t088), ('T099', t099),
]

if __name__ == '__main__':
    targets = sys.argv[1:] if len(sys.argv) > 1 else [n for n, _ in SCENARIOS]
    for name, fn in SCENARIOS:
        if name not in targets and 'ALL' not in targets:
            continue
        try:
            fn()
        except Exception as e:
            print(f'ERROR in {name}: {e}')
        kill_all()
