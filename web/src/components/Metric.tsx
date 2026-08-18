import type { Provenance } from "../types";

export function Metric({ label, metric }: { label: string; metric: Provenance }) {
  const unavailable = metric.value === null;
  return <article className="metric" data-testid="metric"><span>{label}</span><strong>{unavailable ? "Unavailable" : `${metric.value} ${metric.unit}`}</strong><small>{unavailable ? metric.unavailable_reason : `${metric.source} / ${metric.mode} / ${metric.observed_at}`}</small><small>run: {metric.run_id}</small></article>;
}
