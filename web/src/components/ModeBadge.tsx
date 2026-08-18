import type { Health } from "../types";
export function ModeBadge({ health }: { health: Health }) { const labels = { trial: "TRIAL / SIMULATED", "nvml-mock": "NVML MOCK", "real-gpu": "REAL GPU" }; return <span className={`mode mode-${health.mode}`} data-testid="mode-badge">{labels[health.mode]}</span>; }
