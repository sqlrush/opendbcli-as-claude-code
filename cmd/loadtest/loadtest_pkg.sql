SET SERVEROUTPUT ON SIZE UNLIMITED

CREATE OR REPLACE PACKAGE loadtest_pkg AS
  PROCEDURE run_worker(p_duration_sec NUMBER DEFAULT 300, p_worker_id NUMBER DEFAULT 0);
  PROCEDURE start_load(p_workers NUMBER DEFAULT 20, p_duration_sec NUMBER DEFAULT 300);
  PROCEDURE stop_load;
  PROCEDURE report;
END loadtest_pkg;
/

CREATE OR REPLACE PACKAGE BODY loadtest_pkg AS

  PROCEDURE run_worker(p_duration_sec NUMBER DEFAULT 300, p_worker_id NUMBER DEFAULT 0) IS
    v_end_time  DATE := SYSDATE + p_duration_sec / 86400;
    v_id        NUMBER;
    v_cnt       NUMBER;
    v_sum       NUMBER;
    v_payload   VARCHAR2(200);
    v_batch     NUMBER;
    v_rand      NUMBER;
    v_iter      NUMBER := 0;
    v_commit_every CONSTANT NUMBER := 100;
  BEGIN
    WHILE SYSDATE < v_end_time LOOP
      v_iter := v_iter + 1;
      v_rand := MOD(ABS(DBMS_RANDOM.RANDOM), 100);
      v_batch := MOD(ABS(DBMS_RANDOM.RANDOM), 10000);

      -- INSERT (25%)
      IF v_rand < 25 THEN
        SELECT loadtest_seq.NEXTVAL INTO v_id FROM dual;
        INSERT INTO loadtest (id, batch_id, payload, val1, val2)
        VALUES (v_id, v_batch,
                'w' || p_worker_id || '-' || v_id || '-' || DBMS_RANDOM.STRING('A', 30),
                MOD(ABS(DBMS_RANDOM.RANDOM), 10000),
                MOD(ABS(DBMS_RANDOM.RANDOM), 10000));

      -- DELETE (25%)
      ELSIF v_rand < 50 THEN
        DELETE FROM loadtest WHERE batch_id = v_batch AND ROWNUM <= 3;

      -- SELECT (30%)
      ELSIF v_rand < 80 THEN
        IF MOD(v_rand, 2) = 0 THEN
          SELECT COUNT(*), NVL(SUM(val1), 0) INTO v_cnt, v_sum
          FROM loadtest WHERE batch_id BETWEEN v_batch AND v_batch + 200;
        ELSE
          BEGIN
            SELECT id, payload INTO v_id, v_payload
            FROM loadtest WHERE batch_id = v_batch AND ROWNUM = 1;
          EXCEPTION WHEN NO_DATA_FOUND THEN NULL;
          END;
        END IF;

      -- UPDATE (20%)
      ELSE
        UPDATE loadtest SET val1 = MOD(ABS(DBMS_RANDOM.RANDOM), 10000),
               val2 = MOD(ABS(DBMS_RANDOM.RANDOM), 10000)
        WHERE batch_id = v_batch AND ROWNUM <= 3;
      END IF;

      IF MOD(v_iter, v_commit_every) = 0 THEN
        COMMIT;
      END IF;
    END LOOP;
    COMMIT;
  EXCEPTION
    WHEN OTHERS THEN
      COMMIT;
      RAISE;
  END run_worker;

  PROCEDURE start_load(p_workers NUMBER DEFAULT 20, p_duration_sec NUMBER DEFAULT 300) IS
    v_job NUMBER;
  BEGIN
    stop_load;
    DBMS_OUTPUT.PUT_LINE('Starting ' || p_workers || ' workers for ' || p_duration_sec || 's');
    FOR i IN 1..p_workers LOOP
      DBMS_JOB.SUBMIT(
        job       => v_job,
        what      => 'loadtest_pkg.run_worker(' || p_duration_sec || ', ' || i || ');',
        next_date => SYSDATE,
        interval  => NULL
      );
    END LOOP;
    COMMIT;
    DBMS_OUTPUT.PUT_LINE('All ' || p_workers || ' workers submitted.');
  END start_load;

  PROCEDURE stop_load IS
    v_count NUMBER := 0;
  BEGIN
    FOR rec IN (SELECT job FROM user_jobs WHERE what LIKE 'loadtest_pkg.run_worker%') LOOP
      BEGIN
        DBMS_JOB.REMOVE(rec.job);
        v_count := v_count + 1;
      EXCEPTION WHEN OTHERS THEN NULL;
      END;
    END LOOP;
    COMMIT;
    IF v_count > 0 THEN
      DBMS_OUTPUT.PUT_LINE('Removed ' || v_count || ' pending jobs');
    END IF;
  END stop_load;

  PROCEDURE report IS
    v_sessions NUMBER;
    v_count    NUMBER;
    v_size_mb  NUMBER;
  BEGIN
    SELECT COUNT(*) INTO v_sessions
    FROM v$session WHERE username = 'OPENDB_TEST' AND status = 'ACTIVE';
    SELECT COUNT(*) INTO v_count FROM loadtest;
    SELECT NVL(ROUND(bytes/1024/1024), 0) INTO v_size_mb
    FROM user_segments WHERE segment_name = 'LOADTEST' AND ROWNUM = 1;
    DBMS_OUTPUT.PUT_LINE('Active workers: ' || v_sessions);
    DBMS_OUTPUT.PUT_LINE('Table rows:     ' || v_count);
    DBMS_OUTPUT.PUT_LINE('Table size:     ' || v_size_mb || ' MB');
  END report;

END loadtest_pkg;
/

SHOW ERRORS PACKAGE BODY loadtest_pkg;
