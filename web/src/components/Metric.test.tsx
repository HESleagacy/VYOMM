import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Metric } from "./Metric";
const metric = { value: 62, unit: "percent", source: "computed" as const, mode: "trial" as const, observed_at: "2026-08-18T12:00:00Z", run_id: "run-1" };
describe("Metric",()=>{it("renders value and provenance",()=>{render(<Metric label="Confidence" metric={metric}/>);expect(screen.getByText("62 percent")).toBeInTheDocument();expect(screen.getByText(/computed \/ trial/)).toBeInTheDocument()});it("renders unavailable reason",()=>{render(<Metric label="GPU" metric={{...metric,value:null,unavailable_reason:"Unavailable in simulated mode"}}/>);expect(screen.getByText("Unavailable in simulated mode")).toBeInTheDocument()})});
