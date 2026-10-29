import { useUI } from "../ui-preferences";
import type { TaskAnalysisResult } from "../agents";

// Presentation only: the original source-bound message stays unchanged for
// inspection, memory capture and AI disclosure. Feed text is never HTML.
export function watchMessage(text: string) {
  if (text.length > 4000) return;
  try {
    const body = JSON.parse(text.split("\n")[1]);
    const watch = body.Version === "news-watch-run-chat-v1";
    const analysisOnly = body.Version === "agent-analysis-run-chat-v1";
    if ((!watch && !analysisOnly) || typeof body.Task?.Name !== "string")
      return;
    const analysis = body.Run?.Analysis;
    if (
      analysis &&
      (analysis.version !== "task-analysis-v1" ||
        !["completed", "skipped"].includes(analysis.state) ||
        (analysis.reason !== undefined && typeof analysis.reason !== "string"))
    )
      return;
    if (analysisOnly && !analysis && typeof body.Run?.Outcome !== "string")
      return;
    const aiState = [
      "completed",
      "failed",
      "unknown",
      "cancelled",
      "queued",
      "running",
    ].includes(body.AI?.State)
      ? (body.AI.State as string)
      : "";
    let answer = "";
    if (analysis?.state === "completed") {
      if (
        body.AI?.State !== "completed" ||
        typeof body.AI?.Answer !== "string" ||
        !body.AI.Answer
      )
        return;
      answer = body.AI.Answer;
    }
    if (
      watch &&
      (body.Run?.Watch?.rule?.version !== "news-watch-v1" ||
        !Array.isArray(body.Run.Watch.newIds) ||
        body.Run.Watch.newIds.length > 5 ||
        !Array.isArray(body.Run.Headlines ?? []))
    )
      return;
    const headlines = watch ? (body.Run.Headlines ?? []) : [];
    if (
      (watch && headlines.length !== body.Run.Watch.newIds.length) ||
      headlines.some(
        (h: { id?: string; title?: string }, i: number) =>
          h.id !== body.Run.Watch.newIds[i] || typeof h.title !== "string",
      )
    )
      return;
    return {
      name: body.Task.Name as string,
      headlines: headlines as { id: string; title: string }[],
      watch,
      answer,
      analysis: analysis as TaskAnalysisResult | undefined,
      outcome:
        typeof body.Run?.Outcome === "string"
          ? (body.Run.Outcome as string)
          : "",
      aiState,
    };
  } catch {
    return;
  }
}

export function NewsWatchMessage({ text }: { text: string }) {
  const { t } = useUI();
  const watch = watchMessage(text);
  if (!watch) return <p>{text}</p>;
  return (
    <>
      <strong>{watch.name}</strong>
      {watch.watch && (
        <p>{t("New matches: {count}", { count: watch.headlines.length })}</p>
      )}
      {watch.analysis?.state === "completed" && (
        <p className="ai-answer">{watch.answer}</p>
      )}
      {watch.analysis?.state === "skipped" && (
        <p>
          {t(
            watch.analysis.reason === "no-new-matches"
              ? "Nothing new to analyze"
              : "Analysis skipped",
          )}
        </p>
      )}
      {!watch.watch && !watch.analysis && (
        <>
          <p>{watch.outcome}</p>
          {watch.aiState && (
            <small>
              {t("Analysis")}:{" "}
              {t(
                (
                  {
                    completed: "Completed",
                    failed: "Failed",
                    unknown: "Unknown",
                    cancelled: "Cancelled",
                    queued: "Queued",
                    running: "Running",
                  } as Record<string, string>
                )[watch.aiState],
              )}
            </small>
          )}
        </>
      )}
      {watch.headlines.length > 0 && (
        <ul className="watch-headlines">
          {watch.headlines.map((h) => (
            <li key={h.id}>{h.title}</li>
          ))}
        </ul>
      )}
      <details className="task-inspection">
        <summary>{t("Source")}</summary>
        <p>{text}</p>
      </details>
    </>
  );
}
