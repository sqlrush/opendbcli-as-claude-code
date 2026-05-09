import TerminalMockup from "../TerminalMockup";

const DIM = "rgba(237,236,236,0.35)";
const GREEN = "#7ee787";
const YELLOW = "#ffc61c";
const ORANGE = "#ff6b35";

export default function SentinelTerminal() {
  return (
    <TerminalMockup>
      {/* Start command */}
      <div>
        <span style={{ color: GREEN }}>opendb&gt;</span>{" "}
        <span>/sentinel start</span>
      </div>
      <div style={{ color: GREEN }}>✓  哨兵已启动 · 监控 48 项指标</div>

      {/* Separator */}
      <div style={{ borderTop: "1px solid rgba(237,236,236,0.15)", margin: "0.4rem 0" }} />

      {/* Normal sampling */}
      <div style={{ color: DIM }}>09:14:03  采样正常 · 所有指标在基线范围内</div>
      <div style={{ color: DIM }}>09:14:04  采样正常</div>

      {/* Anomaly alert */}
      <div style={{ margin: "0.4rem 0" }}>
        <div>
          <span style={{ color: ORANGE }}>⚡ 09:14:05  异常检测</span>
        </div>
        <div style={{ paddingLeft: "0.5rem" }}>
          <div>
            <span style={{ color: DIM }}>指标  </span>
            <span style={{ color: "#edecec" }}>physical reads</span>
          </div>
          <div>
            <span style={{ color: DIM }}>当前  </span>
            <span style={{ color: ORANGE }}>82,341 blk/s</span>
          </div>
          <div>
            <span style={{ color: DIM }}>基线  </span>
            <span style={{ color: "#edecec" }}>18,200 ± 3,100 blk/s</span>
          </div>
          <div>
            <span style={{ color: DIM }}>阈值  </span>
            <span style={{ color: YELLOW }}>μ + 3σ = 27,500</span>
          </div>
          <div>
            <span style={{ color: DIM }}>持续  </span>
            <span style={{ color: "#edecec" }}>23 秒</span>
          </div>
        </div>
      </div>

      {/* Burst collection */}
      <div style={{ borderTop: "1px solid rgba(237,236,236,0.15)", margin: "0.4rem 0" }} />
      <div style={{ color: YELLOW }}>
        ⟳  触发突发采集 · 收集诊断快照...
      </div>
      <div style={{ color: GREEN }}>✓  快照已保存 · 运行 /rule 开始诊断</div>
    </TerminalMockup>
  );
}
