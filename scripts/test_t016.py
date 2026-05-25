#!/usr/bin/env python3
"""T016 full table scan test with enrichment verification."""
import cx_Oracle, subprocess, threading, time, os

DSN = cx_Oracle.makedsn('127.0.0.1', 1521, service_name='orclpdb1')

# Step 1: Clean
print('Step 1: Clean')
c = cx_Oracle.connect('system', 'OraclePass123', DSN)
cur = c.cursor()
try:
    cur.execute("ALTER SYSTEM SET resource_manager_plan = '' SCOPE=BOTH")
    print('  RM disabled')
except Exception as e:
    print(f'  RM note: {e}')
rows = cur.execute("SELECT p.spid FROM v$session s JOIN v$process p ON s.paddr=p.addr WHERE s.username='TESTUSER'").fetchall()
print(f'  Killing {len(rows)} sessions')
for r in rows:
    os.system('kill -9 %s' % r[0])
c.close()
time.sleep(5)

# Step 2: Warm cache
print('Step 2: Warm cache')
c = cx_Oracle.connect('testuser', 'testpass123', DSN)
cur = c.cursor()
cur.execute("SELECT /*+ FULL(big_test) */ count(*) FROM big_test")
print(f'  big_test rows: {cur.fetchone()[0]}')
c.close()

# Step 3: Start load
print('Step 3: Start 30 threads')
bar = threading.Barrier(31, timeout=60)

FULL_SCAN_SQL = """DECLARE v NUMBER; BEGIN
    LOOP
        SELECT /*+ FULL(big_test) NO_INDEX(big_test) */ count(*) INTO v
        FROM big_test WHERE val = 'findme';
    END LOOP; END;"""

def reader():
    try:
        co = cx_Oracle.connect('testuser', 'testpass123', DSN)
        bar.wait()
        co.cursor().execute(FULL_SCAN_SQL)
    except:
        try:
            bar.wait()
        except:
            pass

for _ in range(30):
    threading.Thread(target=reader, daemon=True).start()
bar.wait()
print('  Threads running')
time.sleep(10)

# Step 4: Verify
c = cx_Oracle.connect('system', 'OraclePass123', DSN)
cur = c.cursor()
cur.execute("SELECT COUNT(*) FROM v$session WHERE username='TESTUSER' AND status='ACTIVE'")
active = cur.fetchone()[0]
print(f'Step 4: Active = {active}')
cur.execute("SELECT event, COUNT(*) FROM v$session WHERE username='TESTUSER' AND status='ACTIVE' GROUP BY event ORDER BY COUNT(*) DESC FETCH FIRST 5 ROWS ONLY")
for r in cur.fetchall():
    print(f'  {r[0]}: {r[1]}')
c.close()

# Step 5: Run opendb
print('Step 5: opendb')
r = subprocess.run(['opendb', '-c', 'oracle', '/rule live'],
                    stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=120)
out = r.stdout.decode('utf-8', 'replace')

# Print full output
print(out)

# Step 6: Verify enrichment data while sessions still alive
c = cx_Oracle.connect('system', 'OraclePass123', DSN)
cur = c.cursor()
cur.execute("SELECT sql_id, COUNT(*) FROM v$session WHERE username='TESTUSER' AND status='ACTIVE' AND sql_id IS NOT NULL GROUP BY sql_id ORDER BY 2 DESC FETCH FIRST 1 ROWS ONLY")
row = cur.fetchone()
if row:
    sid = row[0]
    # Plan query with bind variable (same as liveSQLPlanSQL)
    cur.execute("""SELECT
       MAX(CASE WHEN p.operation='TABLE ACCESS' AND p.options='FULL' THEN 1 ELSE 0 END),
       MAX(CASE WHEN p.operation IN ('SORT ORDER BY','SORT AGGREGATE','SORT GROUP BY') THEN 1 ELSE 0 END),
       MAX(CASE WHEN p.operation='HASH JOIN' THEN 1 ELSE 0 END),
       MAX(CASE WHEN INSTR(p.operation,'INDEX') > 0 THEN 1 ELSE 0 END)
    FROM v$sql_plan p WHERE p.sql_id = :1""", [sid])
    plan = cur.fetchone()
    print(f'VERIFY plan for {sid}: full={plan[0]} sort={plan[1]} hash={plan[2]} idx={plan[3]}')

    cur.execute("SELECT SUM(buffer_gets), SUM(executions), SUM(rows_processed) FROM v$sql WHERE sql_id = :1", [sid])
    stats = cur.fetchone()
    print(f'VERIFY stats for {sid}: bufs={stats[0]} execs={stats[1]} rows={stats[2]}')
else:
    print('VERIFY: no active SQL found')
c.close()

# Step 7: Kill
c = cx_Oracle.connect('system', 'OraclePass123', DSN)
for row in c.cursor().execute("SELECT p.spid FROM v$session s JOIN v$process p ON s.paddr=p.addr WHERE s.username='TESTUSER'").fetchall():
    os.system('kill -9 %s' % row[0])
c.close()
print('Done')
