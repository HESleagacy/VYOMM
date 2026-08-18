import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import App from "./App";
describe("App",()=>{it("shows mode from health fixture",()=>{render(<App/>);expect(screen.getByTestId("mode-badge")).toHaveTextContent("TRIAL / SIMULATED")});it("navigates to required screens",async()=>{render(<App/>);fireEvent.click(screen.getByRole("button",{name:"GPU Resources"}));expect(await screen.findByRole("heading",{name:"GPU resources"})).toBeInTheDocument()})});
