import { useState } from "react";
import fixture from "./fixtures/data.json";
import { Metric } from "./components/Metric";
import { ModeBadge } from "./components/ModeBadge";
import type { AppData, Provenance } from "./types";
import "./styles.css";

const data = fixture as AppData;
const gap = (label: string) => <div className="gap"><b>{label}</b><span>Not provenance-wrapped in API_CONTRACT.md</span></div>;
function Overview() { return <><h1>Operations overview</h1><p className="lede">Contract-backed observability surface. Unwrapped numeric fields are intentionally not rendered as measurements.</p><section className="metrics"><Metric label="Forecast confidence" metric={data.forecast.confidence}/>{gap("Device count")}{gap("Incident count")}</section><section className="panel"><h2>System checks</h2>{Object.entries(data.health.checks).map(([k,v])=><p className="check" key={k}><span>{k}</span><b>{v}</b></p>)}</section></> }
function GPUResources() { return <><h1>GPU resources</h1><p className="lede">Physical GPU readings are unavailable in trial mode unless the API returns provenance.</p><section className="metrics">{gap("GPU utilization")}{gap("GPU memory")}{gap("Inference latency")}</section></> }
function Scenarios() { return <><h1>Scenarios</h1><p className="lede">Deterministic scenario catalog from <code>/api/v1/scenarios</code>.</p><div className="list">{data.scenarios.map(s=><article className="panel" key={s.name}><h2>{s.name}</h2><p>{s.description ?? "No description supplied by contract."}</p><button disabled>Run scenario</button></article>)}</div></> }
function Incidents() { return <><h1>Incidents</h1>{data.incidents.active ? <article className="panel"><h2>{data.incidents.active.root_cause}</h2><p>{data.incidents.active.recommended_action}</p>{gap("Incident confidence")}</article> : <div className="empty">No active incident.</div>}</> }
function Evaluation() { return <><h1>Evaluation</h1><p className="lede">Evaluation reports are produced by the Go evaluator; no fabricated score is shown.</p><div className="empty">No evaluation run loaded.</div></> }
function Environment() { return <><h1>Environment</h1><section className="panel"><p><span>API mode</span> <b>{data.health.mode}</b></p><p><span>Version</span> <b>{data.health.version}</b></p><p><span>Run ID</span> <code>{data.health.run_id}</code></p></section></> }
const pages = { Overview, "GPU Resources": GPUResources, Scenarios, Incidents, Evaluation, Environment };
export default function App() { const [page, setPage] = useState<keyof typeof pages>("Overview"); const Page = pages[page]; return <div className="shell"><header><div className="wordmark">VYOMM <span>NOC OBSERVABILITY</span></div><ModeBadge health={data.health}/></header><nav>{Object.keys(pages).map(p=><button className={p===page?"active":""} onClick={()=>setPage(p as keyof typeof pages)} key={p}>{p}</button>)}</nav><main><Page /></main><footer>source: contract fixture · observed {data.telemetry.server_time}</footer></div> }
