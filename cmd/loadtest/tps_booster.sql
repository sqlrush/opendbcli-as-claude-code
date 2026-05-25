SET SERVEROUTPUT ON SIZE UNLIMITED

-- Create a TPS booster procedure: lightweight I/S/U/D with commit every iteration
CREATE OR REPLACE PROCEDURE loadtest_tps_worker(p_duration_sec NUMBER DEFAULT 300, p_worker_id NUMBER DEFAULT 0) IS
  v_end_time DATE := SYSDATE + p_duration_sec / 86400;
  v_iter     NUMBER := 0;
  v_val      NUMBER;
  v_grp      NUMBER := MOD(900 + p_worker_id, 1000); -- use grp 900+ range (separate from main workers)
  v_target   NUMBER;
  v_max_id   NUMBER;
BEGIN
  SELECT NVL(MAX(id), 1) INTO v_max_id FROM loadtest2;

  WHILE SYSDATE < v_end_time LOOP
    v_iter := v_iter + 1;

    CASE MOD(v_iter, 4)
      -- INSERT 1 row
      WHEN 0 THEN
        INSERT INTO loadtest2 (id, grp, val1, val2)
        VALUES (loadtest2_seq.NEXTVAL, v_grp, MOD(v_iter, 9999), MOD(v_iter+1, 9999));

      -- DELETE 1 row
      WHEN 1 THEN
        DELETE FROM loadtest2 WHERE grp = v_grp AND ROWNUM = 1;

      -- SELECT 1 row
      WHEN 2 THEN
        v_target := MOD(v_iter * 17 + p_worker_id * 131, GREATEST(v_max_id, 1)) + 1;
        BEGIN SELECT val1 INTO v_val FROM loadtest2 WHERE id = v_target;
        EXCEPTION WHEN NO_DATA_FOUND THEN NULL; END;

      -- UPDATE 1 row
      WHEN 3 THEN
        UPDATE loadtest2 SET val1 = MOD(v_iter, 9999) WHERE grp = v_grp AND ROWNUM = 1;
    END CASE;

    -- Commit EVERY iteration for maximum TPS
    COMMIT;
  END LOOP;
  COMMIT;
EXCEPTION
  WHEN OTHERS THEN COMMIT;
END;
/

SHOW ERRORS PROCEDURE loadtest_tps_worker;

-- Launch 80 TPS booster workers via DBMS_JOB
DECLARE
  v_job NUMBER;
BEGIN
  FOR i IN 1..80 LOOP
    DBMS_JOB.SUBMIT(
      job       => v_job,
      what      => 'loadtest_tps_worker(' || 1800 || ', ' || i || ');',
      next_date => SYSDATE,
      interval  => NULL
    );
  END LOOP;
  COMMIT;
  DBMS_OUTPUT.PUT_LINE('80 TPS booster workers submitted');
END;
/
