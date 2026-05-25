# Rule Validation 执行进度

## 当前状态：Round 2 完成 — 准备新增 40 场景

### Round 2 结果（2026-03-27）

优化后重测 20 场景（关闭 Resource Manager），可评分 16 个：

| # | 场景 | R1 | R2 | 变化 | R2 诊断 |
|---|------|-----|-----|------|---------|
| T001 | 锁级联:blocker空闲 | 22 | **78** | +56 | blocker空闲/睡眠+kill+IDLE_TIME ✅ |
| T002 | 锁级联:blocker跑慢SQL | 23 | **65** | +42 | blocker空闲（SQL秒完成） |
| T004 | 多层阻塞链 | 22 | **75** | +53 | 多层阻塞链或死锁 ✅ |
| T005 | 死锁频率判断 | 17 | **40** | +23 | blocker空闲（未识别死锁） |
| T008 | ITL争用 | 28 | N/A | - | SSD秒完成 |
| T009 | Sequence争用(NOCACHE) | 30 | **35** | +5 | row cache lock→并发登录（根因错） |
| T010 | HW争用 | 35 | **30** | -5 | NOCACHE seq掩盖HW |
| T011 | SQ enqueue | 88 | N/A | - | 数据冲突 |
| T012 | Row Cache Lock(DDL) | 38 | **25** | -13 | AWR指标正常（兜底分支回退）|
| T013 | Cursor Pin S(同SQL) | 48 | **72** | +24 | 纯并发热点RC1+mutex调优 ✅ |
| T014 | Cursor Pin S热块 | 50 | **55** | +5 | 纯并发热点RC1 |
| T015 | DDL Lock+DML | 26 | **50** | +24 | blocker空闲（未识别DDL） |
| T020 | 硬解析风暴 | 12 | **30** | +18 | subpool正常（有latch:shared pool） |
| T030 | SELECT FOR UPDATE | 22 | **78** | +56 | blocker空闲+kill ✅ |
| T035 | 递归SQL/CPU | 17 | **25** | +8 | subpool正常 |
| T046 | 表空间满 | 0 | **80** | +80 | SYSTEM 99.4%/UNDOTBS1 95.9% ✅ |
| T050 | INSERT/Redo | 32 | N/A | - | SSD秒完成 |
| T-LFS | Log File Switch | 26 | N/A | - | SSD秒完成 |
| T-CBC | 热块latch | 26 | **50** | +24 | 纯并发热点RC1 |
| T-LIBPIN | Library Cache Pin | 40 | **50** | +10 | 纯并发热点RC1 |

**可评分 16 个：R1 均分 28.0 → R2 均分 52.4（+24.4）**

### 关键改进
- 锁类场景（6个）：22.0 → 64.3（+42.3）
- T046 容量检测：0 → 80（+80）
- T013 cursor pin：48 → 72（+24）

### 仍需优化
1. 死锁检测（T005=40）
2. row cache lock 细分（T009=35, T010=30）
3. 硬解析关联（T020=30, T035=25）
4. DDL 冲突识别（T012=25, T015=50）
5. T012 兜底分支回退（38→25）

### 环境变更
- Resource Manager 已关闭（`ALTER SYSTEM SET RESOURCE_MANAGER_PLAN = ''`）
- Auto Tasks 已禁用

### 测试环境
- SSH: `ssh -p 2222 root@47.251.30.180`
- Oracle 19c CDB, PDB=ORCLPDB1
- 测试用户: testuser/testpass123@localhost:1521/ORCLPDB1
- opendb batch: `opendb -c oracle '/rule live'`
- 已有测试表: lock_test, big_test, hot_insert_test, itl_test, hw_test, commit_test, hot_read_test, parent_test/child_test
- **Resource Manager**: 已关闭
- **注意**: oracle_loadtest.py 会自动重启，测试前需 pkill
