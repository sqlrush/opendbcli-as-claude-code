# Round 4 — 全覆盖测试（2026-03-28）

## 本轮新增测试

### 已完成（6 个新场景）

| 场景 | 分数 | 说明 |
|------|------|------|
| **T086** UNDO_RETENTION=100s | **72** | 正确检测偏小+ORA-01555风险+修复SQL |
| **T087** OPEN_CURSORS=200 | **70** | 正确检测偏小+ORA-01000风险+修复SQL |
| **T089** OPTIMIZER_INDEX_COST_ADJ=50 | **68** | 正确检测非默认值+恢复建议 |
| **T090** DB_FILE_MULTIBLOCK_READ_COUNT=512 | **72** | 正确检测过大+全扫偏好风险 |
| **T091** Checkpoint Incomplete | **70** | redo生成速率太高+增大RedoLog+FAST_START_MTTR |
| **T052** 大批量DML Redo | **65** | buffer busy+checkpoint关联 |

### SSD 环境验证但非目标场景（2 个）

| 场景 | 分数 | 说明 |
|------|------|------|
| **T073** Read by Other Session | **65** | SSD变为cursor:pin S热点，诊断方向正确 |
| **T084** Active Sessions突增 | **60** | 识别了根因（硬解析）但缺active sessions突增视角 |

### 未能测试的场景分类

#### SSD 环境无法产生目标等待事件（21 个）

| 场景 | 原因 |
|------|------|
| T003 blocker等IO | 需慢IO：blocker的IO瞬间完成 |
| T006 FK缺索引TM锁 | SSD秒完成：锁瞬间释放 |
| T007 DDL阻塞DML | SSD秒完成：DDL瞬间完成 |
| T016 全表扫描缺索引 | 需慢IO：全扫不产生IO等待 |
| T017 全表扫描低选择度 | 同上 |
| T023 Hash Join溢出 | SSD秒完成+PGA充足 |
| T024 NL低效 | SSD秒完成 |
| T029 全表扫描 | 需慢IO |
| T036 Buffer Cache冲刷 | 需慢IO |
| T037 Buffer Cache太小 | ASMM自动管理+SSD无IO等待 |
| T041 Free Buffer Waits | 需慢IO (DBWR写慢) |
| T056 Log File Sync存储 | 需慢IO |
| T062 LGWR写延迟 | 需慢IO |
| T064 索引失效 | SSD秒完成 |
| T065 Log Switch抖动 | 需慢IO |
| T066 db file seq read存储 | 需慢IO |
| T067 db file seq read索引 | 需大数据集超RAM |
| T068 db file scattered read | 需慢IO |
| T072 DBWR写入慢 | 需慢IO |
| T074 Direct Path Read | 需大数据集 |
| T075 IO Calibration | 需慢IO |

#### 需特殊环境（14 个）

| 场景 | 需要什么 |
|------|---------|
| T028 SPM锁定次优 | SQL Plan Baseline 操作 |
| T044 SGA Auto-resize | AMM 开启+负载波动 |
| T045 Large Pool+RMAN | RMAN 运行 |
| T049 FRA满 | 归档模式 |
| T054 段空间浪费(HWM) | DELETE后不SHRINK |
| T055 归档目录空间 | 归档模式 |
| T059 Redo Log Space Wait | 小Redo+归档延迟 |
| T060 归档延迟+备库 | Data Guard |
| T061 归档模式大批量 | 归档模式 |
| T063 LOB膨胀 | LOB INSERT |
| T069 网络延迟 | 多节点 |
| T078 Aborted Connects | 快速连接断开 |
| T079 会话泄漏 | 累计连接 |
| T082 PX争用 | SSD秒完成 |

#### 高风险/特殊操作（5 个）

| 场景 | 风险 |
|------|------|
| T070 提交速率骤降(T9) | 需Sentinel T9触发器 |
| T071 Enqueue Wait偏移(T7) | 需Sentinel T7触发器 |
| T083 后台进程等待 | 难以稳定复现 |
| T085 全库Hang | 高风险 |
| T094 Crash Recovery | 需kill -9 Oracle |

## 全量统计更新

### 已测场景（65 个）

| 分段 | 数量 | 场景 |
|------|------|------|
| ≥70 | 20 | T001(78),T002(80),T004(75),T005(68),T009(82),T010(78),T013(72),T014(76),T020(80),T030(78),T046(80),T-CBC(74),T099(69),T086(72),T087(70),T089(68),T090(72),T091(70),T052(65),T073(65) |
| 50-69 | 7 | T021(63),T-LIBPIN(55),T040(55),T042(55),T026(50),T084(60),T058(40) |
| <50 | 5 | T012(45),T-LIBPIN已入50+,T035(25),T031(~30) |
| ~15 估算 | 8 | T016-T019,T027,T029,T031,T036,T038 |
| 0 无诊断 | 18 | T022,T025,T032-T034,T039,T043,T047-T048,T051,T053,T057,T076-T077,T080-T081,T086-T090(已测) |
| N/A | 6 | T008,T011,T015,T050,T-LFS,T088 |

### 未测场景（40 个）

- 21 个需慢IO环境
- 14 个需特殊配置
- 5 个高风险/特殊操作
