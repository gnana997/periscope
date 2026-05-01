import { Link } from "react-router-dom";
import { useCronJobDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import { cn } from "../../../lib/cn";
import { describeCron } from "../../../lib/cron";
import type { CronJobChildJob, JobStatus } from "../../../lib/types";
import { DetailError, DetailLoading } from "../states";
import {
  KV,
  MetaPills,
  SectionTitle,
  StatStrip,
  type StatTone,
} from "./shared";

function statusTone(s: JobStatus): StatTone {
  switch (s) {
    case "Complete":
      return "green";
    case "Failed":
      return "red";
    case "Running":
      return "yellow";
    case "Suspended":
      return "muted";
    default:
      return "neutral";
  }
}

function statusDotClass(s: JobStatus): string {
  switch (s) {
    case "Complete":
      return "bg-green";
    case "Failed":
      return "bg-red";
    case "Running":
      return "bg-accent";
    case "Suspended":
      return "bg-ink-faint";
    default:
      return "bg-yellow";
  }
}

export function CronJobDescribe({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useCronJobDetail(
    cluster,
    ns,
    name,
  );

  if (isLoading) return <DetailLoading />;
  if (isError)
    return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Schedule", value: data.schedule, family: "mono" },
          {
            label: "Suspend",
            value: data.suspend ? "true" : "false",
            tone: data.suspend ? "yellow" : "neutral",
            family: "sans",
          },
          {
            label: "Active",
            value: String(data.active),
            tone: data.active > 0 ? "yellow" : "neutral",
          },
          {
            label: "Last Schedule",
            value: data.lastScheduleTime
              ? ageFrom(data.lastScheduleTime)
              : "—",
            tone: "muted",
          },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />

      <div className="px-5 py-4">
        <dl className="space-y-2">
          <KV label="Schedule" mono>
            {data.schedule}
            <span className="ml-2 text-ink-muted">
              ({describeCron(data.schedule)})
            </span>
          </KV>
          <KV label="Concurrency">{data.concurrencyPolicy}</KV>
          {typeof data.startingDeadlineSeconds === "number" && (
            <KV label="Starting Deadline">
              {data.startingDeadlineSeconds}s
            </KV>
          )}
          <KV label="History">
            <span className="font-mono text-[12px]">
              <span className="text-green">
                {data.successfulJobsHistoryLimit}
              </span>
              <span className="text-ink-faint"> success / </span>
              <span className="text-red">{data.failedJobsHistoryLimit}</span>
              <span className="text-ink-faint"> failed</span>
            </span>
          </KV>
          {data.lastSuccessfulTime && (
            <KV label="Last Success" mono>
              {ageFrom(data.lastSuccessfulTime)} ago
            </KV>
          )}
        </dl>

        <SectionTitle>Recent Jobs</SectionTitle>
        {data.jobs.length === 0 ? (
          <span className="text-[11.5px] text-ink-faint">
            no jobs spawned yet
          </span>
        ) : (
          <ul className="space-y-1.5">
            {data.jobs.map((j) => (
              <ChildJobRow key={j.name} cluster={cluster} ns={ns} job={j} />
            ))}
          </ul>
        )}

        <SectionTitle>Containers (template)</SectionTitle>
        <ul className="space-y-2">
          {data.containers.map((c) => (
            <li
              key={c.name}
              className="rounded-md border border-border bg-surface-2/40 px-3 py-2"
            >
              <div className="font-mono text-[12.5px] font-medium text-ink">
                {c.name}
              </div>
              <div
                className="mt-1 truncate font-mono text-[11.5px] text-ink-muted"
                title={c.image}
              >
                {c.image}
              </div>
            </li>
          ))}
        </ul>

        <SectionTitle>Labels</SectionTitle>
        <MetaPills map={data.labels} />

        <SectionTitle>Annotations</SectionTitle>
        <MetaPills map={data.annotations} />
      </div>
    </div>
  );
}

function ChildJobRow({
  cluster,
  ns,
  job,
}: {
  cluster: string;
  ns: string;
  job: CronJobChildJob;
}) {
  const target =
    `/clusters/${encodeURIComponent(cluster)}/jobs` +
    `?ns=${encodeURIComponent(ns)}` +
    `&sel=${encodeURIComponent(job.name)}` +
    `&selNs=${encodeURIComponent(ns)}` +
    `&tab=describe`;

  const startedAgo = job.startTime ? `${ageFrom(job.startTime)} ago` : "—";

  return (
    <li>
      <Link
        to={target}
        className="group flex items-center gap-3 rounded-md border border-border bg-surface-2/40 px-3 py-1.5 transition-colors hover:border-border-strong hover:bg-surface-2"
      >
        <span className={cn("block size-1.5 rounded-full", statusDotClass(job.status))} />
        <span className="truncate font-mono text-[12px] text-ink">
          {job.name}
        </span>
        <span
          className={cn(
            "font-mono text-[11.5px]",
            statusTone(job.status) === "green"
              ? "text-green"
              : statusTone(job.status) === "red"
                ? "text-red"
                : statusTone(job.status) === "yellow"
                  ? "text-yellow"
                  : "text-ink-muted",
          )}
        >
          {job.status}
        </span>
        <span className="ml-auto flex items-center gap-3 font-mono text-[11px] text-ink-muted tabular">
          <span>{job.completions}</span>
          <span>{job.duration ?? "—"}</span>
          <span>{startedAgo}</span>
        </span>
      </Link>
    </li>
  );
}
