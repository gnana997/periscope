import { describe, expect, it } from "vitest";
import { ApiError } from "./api";
import {
  accumulatePodSummaries,
  aggregateClaimSeverity,
  collectDigests,
  collectDigestsAcrossPods,
  containerScanState,
  countSeverities,
  countVulnerable,
  coverageToState,
  dedupContainersByDigest,
  extractInstanceId,
  humanizeAge,
  isBannerVisible,
  isInspectorDisabled,
  podKey,
  summarizePodRow,
} from "./cve";
import type {
  CveContainerRow,
  CveFinding,
  CvePodRow,
  CvePodsResp,
  CveSeverityCounts,
  CveStatusResp,
  Pod,
} from "./types";

// ── Fixtures ───────────────────────────────────────────────────────

const ZERO_COUNTS: CveSeverityCounts = {
  critical: 0,
  high: 0,
  medium: 0,
  low: 0,
  informational: 0,
};

function counts(
  c = 0,
  h = 0,
  m = 0,
  l = 0,
  i = 0,
): CveSeverityCounts {
  return { critical: c, high: h, medium: m, low: l, informational: i };
}

function podRow(
  ns: string,
  name: string,
  rolled: CveSeverityCounts = ZERO_COUNTS,
  coverage: "full" | "partial" | "none" = "full",
  containers: CveContainerRow[] = [],
): CvePodRow {
  return {
    namespace: ns,
    name,
    containers,
    rolledUpSeverityCounts: rolled,
    scanCoverage: coverage,
  };
}

function page(pods: CvePodRow[], next?: string): CvePodsResp {
  return { pods, next, inspectorEnabled: true, hydrated: true };
}

function container(
  name: string,
  digest?: string,
  image = `ecr.amazonaws.com/${name}:latest`,
  scanState: "scanned" | "pending" | "non-ecr" = "scanned",
  c: CveSeverityCounts = ZERO_COUNTS,
): CveContainerRow {
  return {
    name,
    image,
    digest,
    scanState,
    severityCounts: c,
  };
}

function finding(severity: string): CveFinding {
  return { cve: `CVE-${severity}`, severity } as CveFinding;
}

// ── extractInstanceId ──────────────────────────────────────────────

describe("extractInstanceId", () => {
  it("returns empty string for undefined / null", () => {
    expect(extractInstanceId(undefined)).toBe("");
    expect(extractInstanceId(null)).toBe("");
    expect(extractInstanceId("")).toBe("");
  });

  it("parses managed-nodegroup EKS providerID", () => {
    expect(extractInstanceId("aws:///us-east-1a/i-0abc123def456")).toBe(
      "i-0abc123def456",
    );
  });

  it("parses Karpenter NodeClaim providerID", () => {
    expect(extractInstanceId("aws:///eu-west-2c/i-0deadbeef")).toBe(
      "i-0deadbeef",
    );
  });

  it("returns empty for non-AWS providers (kind, GCE, bare-metal)", () => {
    expect(extractInstanceId("kind://docker/kind/kind-control-plane")).toBe("");
    expect(extractInstanceId("gce://my-project/us-central1-a/gke-foo")).toBe("");
  });

  it("returns empty for malformed providerID", () => {
    expect(extractInstanceId("aws:///us-east-1a/")).toBe("");
    expect(extractInstanceId("aws:///us-east-1a/notaninstance")).toBe("");
    expect(extractInstanceId("i-0abc123")).toBe("");
  });
});

// ── podKey ─────────────────────────────────────────────────────────

describe("podKey", () => {
  it("formats ns/name", () => {
    expect(podKey("default", "my-pod")).toBe("default/my-pod");
  });

  it("does not collapse empty fields (defensive — backend always supplies both)", () => {
    expect(podKey("", "x")).toBe("/x");
    expect(podKey("kube-system", "")).toBe("kube-system/");
  });
});

// ── summarizePodRow + accumulatePodSummaries ───────────────────────

describe("summarizePodRow", () => {
  it("projects /cve/pods row into the chip-column shape", () => {
    const row = podRow("ns1", "p1", counts(1, 2, 3), "partial");
    expect(summarizePodRow(row)).toEqual({
      namespace: "ns1",
      name: "p1",
      counts: counts(1, 2, 3),
      coverage: "partial",
    });
  });
});

describe("accumulatePodSummaries", () => {
  it("returns empty Map for no pages", () => {
    expect(accumulatePodSummaries([])).toEqual(new Map());
  });

  it("returns empty Map for pages with no pods", () => {
    expect(accumulatePodSummaries([page([]), page([])]).size).toBe(0);
  });

  it("folds a single page into a Map keyed by podKey", () => {
    const out = accumulatePodSummaries([
      page([podRow("ns1", "a", counts(2)), podRow("ns2", "b", counts(0, 1))]),
    ]);
    expect(out.size).toBe(2);
    expect(out.get("ns1/a")?.counts.critical).toBe(2);
    expect(out.get("ns2/b")?.counts.high).toBe(1);
  });

  it("folds across multiple pages", () => {
    const out = accumulatePodSummaries([
      page([podRow("ns1", "a")], "cursor-1"),
      page([podRow("ns1", "b")], "cursor-2"),
      page([podRow("ns2", "c")]),
    ]);
    expect(out.size).toBe(3);
    expect(out.has("ns1/a")).toBe(true);
    expect(out.has("ns1/b")).toBe(true);
    expect(out.has("ns2/c")).toBe(true);
  });

  it("later pages overwrite earlier ones on key collision (fresher wins)", () => {
    const out = accumulatePodSummaries([
      page([podRow("ns1", "a", counts(5))]),
      page([podRow("ns1", "a", counts(1))]),
    ]);
    expect(out.get("ns1/a")?.counts.critical).toBe(1);
  });
});

// ── countSeverities ────────────────────────────────────────────────

describe("countSeverities", () => {
  it("returns zero bucket for empty findings", () => {
    expect(countSeverities([])).toEqual(ZERO_COUNTS);
  });

  it("tallies one of each", () => {
    const out = countSeverities([
      finding("CRITICAL"),
      finding("HIGH"),
      finding("MEDIUM"),
      finding("LOW"),
      finding("INFORMATIONAL"),
    ]);
    expect(out).toEqual(counts(1, 1, 1, 1, 1));
  });

  it("treats INFO as INFORMATIONAL alias", () => {
    expect(countSeverities([finding("INFO"), finding("INFO")])).toEqual(
      counts(0, 0, 0, 0, 2),
    );
  });

  it("is case-insensitive on the severity string", () => {
    expect(countSeverities([finding("critical"), finding("High")])).toEqual(
      counts(1, 1),
    );
  });

  it("drops unknown severities without crashing", () => {
    expect(
      countSeverities([
        finding("CATASTROPHIC"),
        finding(""),
        finding("CRITICAL"),
      ]),
    ).toEqual(counts(1));
  });
});

// ── countVulnerable ────────────────────────────────────────────────

describe("countVulnerable", () => {
  const a: Pod = { namespace: "ns1", name: "a" } as Pod;
  const b: Pod = { namespace: "ns1", name: "b" } as Pod;
  const c: Pod = { namespace: "ns2", name: "c" } as Pod;

  it("returns 0 for undefined map", () => {
    expect(countVulnerable([a, b], undefined)).toBe(0);
  });

  it("returns 0 when no pod has critical or high", () => {
    const m = new Map([
      ["ns1/a", { counts: counts(0, 0, 5, 3) }],
      ["ns1/b", { counts: ZERO_COUNTS }],
    ]);
    expect(countVulnerable([a, b], m)).toBe(0);
  });

  it("counts pods with critical findings", () => {
    const m = new Map([["ns1/a", { counts: counts(2) }]]);
    expect(countVulnerable([a, b, c], m)).toBe(1);
  });

  it("counts pods with high but no critical findings", () => {
    const m = new Map([["ns2/c", { counts: counts(0, 3) }]]);
    expect(countVulnerable([a, b, c], m)).toBe(1);
  });

  it("ignores pods missing from the map", () => {
    expect(countVulnerable([a, b, c], new Map())).toBe(0);
  });
});

// ── aggregateClaimSeverity ─────────────────────────────────────────

describe("aggregateClaimSeverity", () => {
  it("returns zero counts for no claims", () => {
    expect(aggregateClaimSeverity([], new Map())).toEqual(ZERO_COUNTS);
  });

  it("rolls up matched claims and skips unmatched ones", () => {
    const map = new Map<string, CveSeverityCounts>([
      ["i-aaaa", counts(2, 1)],
      ["i-bbbb", counts(0, 0, 3)],
    ]);
    const sum = aggregateClaimSeverity(
      [
        { providerID: "aws:///us-east-1a/i-aaaa" },
        { providerID: "aws:///us-east-1b/i-bbbb" },
        { providerID: "aws:///us-east-1a/i-cccc" }, // not in map
        { providerID: undefined }, // pre-Initialized claim
      ],
      map,
    );
    expect(sum).toEqual(counts(2, 1, 3));
  });

  it("ignores claims with unparseable providerID", () => {
    const map = new Map([["i-aaaa", counts(5)]]);
    const sum = aggregateClaimSeverity(
      [{ providerID: "kind://docker/foo" }, { providerID: "" }],
      map,
    );
    expect(sum).toEqual(ZERO_COUNTS);
  });
});

// ── coverageToState ────────────────────────────────────────────────

describe("coverageToState", () => {
  it("full → has-findings (chip downshifts to clean on zero counts)", () => {
    expect(coverageToState("full")).toBe("has-findings");
  });

  it("partial → partial", () => {
    expect(coverageToState("partial")).toBe("partial");
  });

  it("none → non-ecr", () => {
    expect(coverageToState("none")).toBe("non-ecr");
  });
});

// ── containerScanState ─────────────────────────────────────────────

describe("containerScanState", () => {
  it("non-ecr container → non-ecr", () => {
    expect(containerScanState(container("c1", undefined, undefined, "non-ecr"))).toBe(
      "non-ecr",
    );
  });

  it("pending container → pending", () => {
    expect(containerScanState(container("c1", "sha256:abc", undefined, "pending"))).toBe(
      "pending",
    );
  });

  it("scanned + counts > 0 → has-findings", () => {
    expect(
      containerScanState(
        container("c1", "sha256:abc", undefined, "scanned", counts(0, 1)),
      ),
    ).toBe("has-findings");
  });

  it("scanned + zero counts → clean", () => {
    expect(containerScanState(container("c1", "sha256:abc"))).toBe("clean");
  });

  it("scanned with no severityCounts → clean (defensive)", () => {
    const c: CveContainerRow = {
      name: "x",
      image: "x:1",
      digest: "sha256:abc",
      scanState: "scanned",
    };
    expect(containerScanState(c)).toBe("clean");
  });
});

// ── collectDigests / collectDigestsAcrossPods ──────────────────────

describe("collectDigests", () => {
  it("returns empty for no containers", () => {
    expect(collectDigests([])).toEqual([]);
  });

  it("dedups digests across containers", () => {
    const a = container("a", "sha256:111");
    const b = container("b", "sha256:222");
    const dup = container("c", "sha256:111");
    expect(collectDigests([a, b, dup]).sort()).toEqual([
      "sha256:111",
      "sha256:222",
    ]);
  });

  it("skips containers without a digest", () => {
    const a = container("a", "sha256:111");
    const noDigest: CveContainerRow = {
      name: "side",
      image: "non-ecr.example.com/foo:v1",
      scanState: "non-ecr",
    };
    expect(collectDigests([a, noDigest])).toEqual(["sha256:111"]);
  });
});

describe("collectDigestsAcrossPods", () => {
  it("dedups across replica pods", () => {
    const replicas = [
      podRow("ns", "p1", undefined, "full", [container("c", "sha256:abc")]),
      podRow("ns", "p2", undefined, "full", [container("c", "sha256:abc")]),
      podRow("ns", "p3", undefined, "full", [container("c", "sha256:def")]),
    ];
    expect(collectDigestsAcrossPods(replicas).sort()).toEqual([
      "sha256:abc",
      "sha256:def",
    ]);
  });
});

// ── dedupContainersByDigest ────────────────────────────────────────

describe("dedupContainersByDigest", () => {
  it("returns empty for no pods", () => {
    expect(dedupContainersByDigest([])).toEqual([]);
  });

  it("collapses identical (name, digest) into one row with podCount", () => {
    const pods = [
      podRow("ns", "p1", undefined, "full", [container("app", "sha256:abc")]),
      podRow("ns", "p2", undefined, "full", [container("app", "sha256:abc")]),
      podRow("ns", "p3", undefined, "full", [container("app", "sha256:abc")]),
    ];
    const out = dedupContainersByDigest(pods);
    expect(out).toHaveLength(1);
    expect(out[0].name).toBe("app");
    expect(out[0].digest).toBe("sha256:abc");
    expect(out[0].podCount).toBe(3);
  });

  it("keeps same-name different-digest containers separate (mid-rollout)", () => {
    const pods = [
      podRow("ns", "p1", undefined, "full", [container("app", "sha256:old")]),
      podRow("ns", "p2", undefined, "full", [container("app", "sha256:new")]),
    ];
    const out = dedupContainersByDigest(pods);
    expect(out).toHaveLength(2);
    const digests = out.map((d) => d.digest).sort();
    expect(digests).toEqual(["sha256:new", "sha256:old"]);
  });

  it("falls back to image when digest is missing", () => {
    const c1: CveContainerRow = {
      name: "side",
      image: "non-ecr.example.com/foo:v1",
      scanState: "non-ecr",
    };
    const c2: CveContainerRow = {
      name: "side",
      image: "non-ecr.example.com/foo:v1",
      scanState: "non-ecr",
    };
    const out = dedupContainersByDigest([
      podRow("ns", "p1", undefined, "full", [c1]),
      podRow("ns", "p2", undefined, "full", [c2]),
    ]);
    expect(out).toHaveLength(1);
    expect(out[0].podCount).toBe(2);
  });
});

// ── humanizeAge ────────────────────────────────────────────────────

describe("humanizeAge", () => {
  // Pin "now" to a known timestamp so the relative output is
  // deterministic regardless of when the test runs.
  const NOW = Date.parse("2026-05-11T12:00:00Z");

  it("seconds", () => {
    expect(humanizeAge("2026-05-11T11:59:30Z", NOW)).toBe("30s ago");
  });

  it("minutes", () => {
    expect(humanizeAge("2026-05-11T11:45:00Z", NOW)).toBe("15m ago");
  });

  it("hours", () => {
    expect(humanizeAge("2026-05-11T09:00:00Z", NOW)).toBe("3h ago");
  });

  it("days", () => {
    expect(humanizeAge("2026-05-09T12:00:00Z", NOW)).toBe("2d ago");
  });

  it("future timestamps clamp to 0s ago (defensive, clock skew)", () => {
    expect(humanizeAge("2026-05-11T12:01:00Z", NOW)).toBe("0s ago");
  });

  it("invalid ISO returns the input string unchanged", () => {
    expect(humanizeAge("not-a-date", NOW)).toBe("not-a-date");
  });
});

// ── isBannerVisible ────────────────────────────────────────────────

describe("isBannerVisible", () => {
  const enabled: CveStatusResp = { inspectorEnabled: true } as CveStatusResp;
  const disabled: CveStatusResp = { inspectorEnabled: false } as CveStatusResp;

  it("hides when cluster is empty", () => {
    expect(
      isBannerVisible({ cluster: "", status: disabled, dismissed: false }),
    ).toBe(false);
  });

  it("hides when status is still loading", () => {
    expect(
      isBannerVisible({ cluster: "c1", status: undefined, dismissed: false }),
    ).toBe(false);
  });

  it("hides when Inspector v2 is enabled", () => {
    expect(
      isBannerVisible({ cluster: "c1", status: enabled, dismissed: false }),
    ).toBe(false);
  });

  it("hides when the operator dismissed it", () => {
    expect(
      isBannerVisible({ cluster: "c1", status: disabled, dismissed: true }),
    ).toBe(false);
  });

  it("shows when cluster is present, status loaded, inspector off, not dismissed", () => {
    expect(
      isBannerVisible({ cluster: "c1", status: disabled, dismissed: false }),
    ).toBe(true);
  });
});

// ── isInspectorDisabled ────────────────────────────────────────────

describe("isInspectorDisabled", () => {
  it("returns false for non-ApiError values", () => {
    expect(isInspectorDisabled(undefined)).toBe(false);
    expect(isInspectorDisabled(new Error("boom"))).toBe(false);
    expect(isInspectorDisabled("oops")).toBe(false);
  });

  it("returns false for ApiError today (stub — backend uses 200 + inspectorEnabled:false)", () => {
    expect(isInspectorDisabled(new ApiError("nope", 412))).toBe(false);
    expect(isInspectorDisabled(new ApiError("nope", 500))).toBe(false);
  });
});
