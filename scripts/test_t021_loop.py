#!/usr/bin/env python3
"""T021: Hard parse storm with infinite loop for stable capture."""
import cx_Oracle, subprocess, threading, time, os

DSN = cx_Oracle.makedsn('127.0.0.1', 1521, service_name='orclpdb1')
OPENDB = '/root/opendb/opendb'

# Kill old sessions
c = cx_Oracle.connect('system', 'OraclePass123', DSN)
for r in c.cursor().execute("SELECT p.spid FROM v$session s JOIN v$process p ON s.paddr=p.addr WHERE s.username='TESTUSER'").fetchall():
    os.system('kill -9 %s' % r[0])
c.close()
time.sleep(3)

# Start 30 threads doing infinite hard parse
HARD_PARSE_SQL = """DECLARE v NUMBER; BEGIN
    LOOP
        EXECUTE IMMEDIATE 'SELECT count(*) FROM big_test WHERE id = ' || TRUNC(DBMS_RANDOM.VALUE * 100000) INTO v;
    END LOOP; END;"""

bar = threading.Barrier(31, timeout=60)
def worker():
    try:
        co = cx_Oracle.connect('testuser', 'testpass123', DSN)
        bar.wait()
        co.cursor().execute(HARD_PARSE_SQL)
    except:
        try: bar.wait()
        except: pass

for _ in range(30):
    threading.Thread(target=worker, daemon=True).start()
bar.wait()
print('30 hard parse threads running')
time.sleep(10)

# Verify
c = cx_Oracle.connect('system', 'OraclePass123', DSN)
cur = c.cursor()
cur.execute("SELECT COUNT(*) FROM v$session WHERE username='TESTUSER' AND status='ACTIVE'")
print(f'Active: {cur.fetchone()[0]}')
c.close()

# Run opendb
r = subprocess.run([OPENDB, '-c', 'oracle', '/rule live'],
                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=120)
out = r.stdout.decode('utf-8', 'replace')
print(out)

# Kill
c = cx_Oracle.connect('system', 'OraclePass123', DSN)
for row in c.cursor().execute("SELECT p.spid FROM v$session s JOIN v$process p ON s.paddr=p.addr WHERE s.username='TESTUSER'").fetchall():
    os.system('kill -9 %s' % row[0])
c.close()
print('Done')
