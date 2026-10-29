import { useUI } from "../ui-preferences";

// Presentation only: the original source-bound message stays unchanged for
// inspection, memory capture and AI disclosure. Feed text is never HTML.
export function watchMessage(text: string) {
  if (text.length > 4000) return;
  try {
    const body = JSON.parse(text.split("\n")[1]);
    if (
      body.Version !== "news-watch-run-chat-v1" ||
      typeof body.Task?.Name !== "string" ||
      body.Run?.Watch?.rule?.version !== "news-watch-v1" ||
      !Array.isArray(body.Run.Watch.newIds) ||
      body.Run.Watch.newIds.length > 5 ||
      !Array.isArray(body.Run.Headlines ?? [])
    )
      return;
    const headlines = body.Run.Headlines ?? [];
    if (
      headlines.length !== body.Run.Watch.newIds.length ||
      headlines.some(
        (h: { id?: string; title?: string }, i: number) =>
          h.id !== body.Run.Watch.newIds[i] || typeof h.title !== "string",
      )
    )
      return;
    return {
      name: body.Task.Name as string,
      headlines: headlines as { id: string; title: string }[],
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
      <p>{t("New matches: {count}", { count: watch.headlines.length })}</p>
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
