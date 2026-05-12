import { describe, expect, it } from "vitest";
import {
  DEFAULT_LOGS_STATE,
  paramsToState,
  stateToParams,
  TAIL_DEFAULT,
  type LogsViewState,
} from "./logsState";

describe("DEFAULT_LOGS_STATE", () => {
  it("ships wrap=true so fresh log views wrap by default", () => {
    // Teammate request: wrap-off is hostile for stack traces / long
    // structured-log lines. Operators were silently truncated; this
    // flip makes wrap the default.
    expect(DEFAULT_LOGS_STATE.wrap).toBe(true);
  });

  it("still defaults follow=true and timestamps=true", () => {
    // Belt-and-suspenders: nothing else regressed when we flipped wrap.
    expect(DEFAULT_LOGS_STATE.follow).toBe(true);
    expect(DEFAULT_LOGS_STATE.timestamps).toBe(true);
  });
});

describe("stateToParams + paramsToState — wrap roundtrip", () => {
  // The contract for these helpers: only non-default fields land in
  // the URL. Now that wrap defaults true, the param is present only
  // when the operator has explicitly turned wrap OFF.

  it("default state produces an empty URL (no wrap param)", () => {
    const out = stateToParams(DEFAULT_LOGS_STATE);
    expect(out.has("wrap")).toBe(false);
    expect(out.toString()).toBe("");
  });

  it("emits wrap=false when the operator opts out", () => {
    const state: LogsViewState = { ...DEFAULT_LOGS_STATE, wrap: false };
    const out = stateToParams(state);
    expect(out.get("wrap")).toBe("false");
  });

  it("URL without wrap param parses back as wrap=true (new default)", () => {
    // This is the critical case: existing bookmarked URLs from before
    // the default flip have no wrap param. After the flip they should
    // read as wrap=true, matching the new default.
    const parsed = paramsToState(new URLSearchParams(""));
    expect(parsed.wrap).toBe(true);
  });

  it("URL with explicit wrap=false parses back as wrap=false", () => {
    // Operators who shared a no-wrap URL keep that behaviour.
    const parsed = paramsToState(new URLSearchParams("wrap=false"));
    expect(parsed.wrap).toBe(false);
  });

  it("legacy URL with wrap=true (pre-flip explicit opt-in) still parses true", () => {
    // Pre-flip URLs that explicitly requested wrap=true are now
    // redundant but harmless — parsing should still produce wrap=true.
    const parsed = paramsToState(new URLSearchParams("wrap=true"));
    expect(parsed.wrap).toBe(true);
  });

  it("round-trips wrap=false through serialization", () => {
    const original: LogsViewState = { ...DEFAULT_LOGS_STATE, wrap: false };
    const back = paramsToState(stateToParams(original));
    expect(back.wrap).toBe(false);
  });

  it("round-trips full default state with no params and no diffs", () => {
    const back = paramsToState(stateToParams(DEFAULT_LOGS_STATE));
    expect(back).toEqual(DEFAULT_LOGS_STATE);
  });

  it("tail/since defaults still honored alongside wrap default", () => {
    // Sanity: the changes to wrap shouldn't have touched other
    // default-handling. tailLines defaults to TAIL_DEFAULT, sinceSeconds
    // to null; neither should be in the URL of a default state.
    const out = stateToParams(DEFAULT_LOGS_STATE);
    expect(out.has("tail")).toBe(false);
    expect(out.has("since")).toBe(false);
    const back = paramsToState(out);
    expect(back.tailLines).toBe(TAIL_DEFAULT);
    expect(back.sinceSeconds).toBeNull();
  });
});
