SET SERVEROUTPUT ON SIZE UNLIMITED

CREATE OR REPLACE PACKAGE BODY loadtest_pkg AS

  PROCEDURE run_worker(p_duration_sec NUMBER DEFAULT 300, p_worker_id NUMBER DEFAULT 0) IS
    v_end_time    DATE := SYSDATE + p_duration_sec / 86400;
    v_iter        NUMBER := p_worker_id * 10000000;
    v_val         NUMBER;
    v_max_id      NUMBER;
    v_op          NUMBER;
    v_target      NUMBER;
    v_approx_rows NUMBER;
    v_max_rows    CONSTANT NUMBER := 400000;
    v_bsz         CONSTANT NUMBER := 20;
  BEGIN
    SELECT NVL(MAX(id), 1) INTO v_max_id FROM loadtest2;
    SELECT COUNT(*) INTO v_approx_rows FROM loadtest2;

    WHILE SYSDATE < v_end_time LOOP
      v_iter := v_iter + 1;
      v_op := MOD(v_iter, 20);

      CASE
        -- BULK INSERT (20%): use INSERT..SELECT for correct sequence IDs
        WHEN v_op <= 3 THEN
          INSERT INTO loadtest2 (id, grp, val1, val2)
          SELECT loadtest2_seq.NEXTVAL,
                 MOD(loadtest2_seq.CURRVAL, 1000),
                 MOD(v_iter * 7 + LEVEL, 9999),
                 MOD(v_iter * 13 + LEVEL, 9999)
          FROM dual CONNECT BY LEVEL <= v_bsz;
          v_approx_rows := v_approx_rows + v_bsz;
          v_max_id := v_max_id + v_bsz;

        -- DELETE (20%): keep table bounded, delete by PK range
        WHEN v_op <= 7 THEN
          v_target := MOD(v_iter * 31 + p_worker_id * 7, GREATEST(v_max_id, 1)) + 1;
          DELETE FROM loadtest2
          WHERE id BETWEEN v_target AND v_target + v_bsz - 1;
          v_approx_rows := v_approx_rows - SQL%ROWCOUNT;

        -- PK SELECT (25%): point lookup
        WHEN v_op <= 12 THEN
          v_target := MOD(v_iter * 17 + p_worker_id * 13, GREATEST(v_max_id, 1)) + 1;
          BEGIN
            SELECT val1 INTO v_val FROM loadtest2 WHERE id = v_target;
          EXCEPTION WHEN NO_DATA_FOUND THEN NULL;
          END;

        -- RANGE SCAN (10%): index scan + aggregate
        WHEN v_op <= 14 THEN
          SELECT COUNT(*) INTO v_val
          FROM loadtest2
          WHERE grp = MOD(v_iter, 1000);

        -- UPDATE (25%): update by PK range
        WHEN v_op >= 15 THEN
          v_target := MOD(v_iter * 23 + p_worker_id * 11, GREATEST(v_max_id, 1)) + 1;
          UPDATE loadtest2
          SET val1 = MOD(v_iter, 9999), val2 = MOD(v_iter + 1, 9999)
          WHERE id BETWEEN v_target AND v_target + v_bsz - 1;
      END CASE;

      -- COMMIT every iteration for maximum TPS
      COMMIT;

      -- Emergency space guard every 1000 iterations
      IF MOD(v_iter, 1000) = 0 AND v_approx_rows > v_max_rows THEN
        DELETE FROM loadtest2 WHERE ROWNUM <= 50000;
        v_approx_rows := v_approx_rows - SQL%ROWCOUNT;
        COMMIT;
        SELECT NVL(MAX(id), 1) INTO v_max_id FROM loadtest2;
      END IF;
    END LOOP;
    COMMIT;
  EXCEPTION
    WHEN OTHERS THEN
      COMMIT;
  END run_worker;

  PROCEDURE start_load(p_workers NUMBER DEFAULT 30, p_duration_sec NUMBER DEFAULT 300) IS
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
    SELECT COUNT(*) INTO v_count FROM loadtest2;
    SELECT NVL(ROUND(bytes/1024/1024), 0) INTO v_size_mb
    FROM user_segments WHERE segment_name = 'LOADTEST2' AND ROWNUM = 1;
    DBMS_OUTPUT.PUT_LINE('Active workers: ' || v_sessions);
    DBMS_OUTPUT.PUT_LINE('Table rows:     ' || v_count);
    DBMS_OUTPUT.PUT_LINE('Table size:     ' || v_size_mb || ' MB');
  END report;

END loadtest_pkg;
/

SHOW ERRORS PACKAGE BODY loadtest_pkg;
