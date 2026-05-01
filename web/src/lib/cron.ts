// Dependency-free cron schedule describer for tooltips.
// Recognizes the common Kubernetes patterns and the @-aliases; falls
// back to the raw expression for anything exotic. Intentionally narrow
// — we don't need a full cron parser, just a friendlier tooltip.

const ALIASES: Record<string, string> = {
  "@yearly": "once a year (Jan 1, 00:00)",
  "@annually": "once a year (Jan 1, 00:00)",
  "@monthly": "once a month (day 1, 00:00)",
  "@weekly": "once a week (Sunday, 00:00)",
  "@daily": "once a day (00:00)",
  "@midnight": "once a day (00:00)",
  "@hourly": "every hour",
};

export function describeCron(expr: string): string {
  if (!expr) return "";
  const trimmed = expr.trim();

  if (ALIASES[trimmed]) return ALIASES[trimmed];

  const parts = trimmed.split(/\s+/);
  if (parts.length !== 5) return trimmed;

  const [minute, hour, dom, month, dow] = parts;
  const allDays = dom === "*" && month === "*" && dow === "*";

  // every N minutes
  const stepMin = stepOf(minute);
  if (stepMin && hour === "*" && allDays) {
    return `every ${stepMin} minute${stepMin === 1 ? "" : "s"}`;
  }

  // every N hours
  const stepHr = stepOf(hour);
  if (minute === "0" && stepHr && allDays) {
    return `every ${stepHr} hour${stepHr === 1 ? "" : "s"}`;
  }

  // daily at HH:MM
  if (isNum(minute) && isNum(hour) && allDays) {
    return `daily at ${pad(hour)}:${pad(minute)}`;
  }

  // weekly on day at HH:MM
  if (isNum(minute) && isNum(hour) && dom === "*" && month === "*" && isNum(dow)) {
    return `weekly on ${dayName(parseInt(dow, 10))} at ${pad(hour)}:${pad(minute)}`;
  }

  // hourly at MM (e.g. "15 * * * *")
  if (isNum(minute) && hour === "*" && allDays) {
    return `hourly at :${pad(minute)}`;
  }

  return trimmed;
}

function stepOf(field: string): number | null {
  const m = field.match(/^\*\/(\d+)$/);
  return m ? parseInt(m[1], 10) : null;
}

function isNum(field: string): boolean {
  return /^\d+$/.test(field);
}

function pad(s: string): string {
  return s.padStart(2, "0");
}

function dayName(n: number): string {
  return ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"][n % 7];
}
