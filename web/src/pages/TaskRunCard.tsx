import { swapState } from "../onchain";
import { SwapResultFacts, SwapRecoveryActions } from "./OnchainSwap";
import { miningState } from "../wen-mining";
import { WENMiningResultFacts, WENMiningRecoveryActions } from "./WENMining";
import { resolveLanguage, translate } from "../ui-language";
import { useUI } from "../ui-preferences";
import type { AgentRun, AgentTask, AgentView } from "../agents";
import { useWorkspace } from "../workspace";
import { Badge } from "../components/shared";
import { safeHttpsHref } from "../agents-v2";
import { PaperResultFacts, paperOutcome } from "./AgentStrategies";

const displayLocale = () =>
  typeof document === "undefined" ? "en" : document.documentElement.lang;
const taskCopy = (key: string, values?: Record<string, string | number>) =>
  translate(resolveLanguage("system", [displayLocale()]), key, values);
const formatDate = (at: number) =>
  at ? new Date(at * 1000).toLocaleString(displayLocale()) : "—";

export function TaskRunCard({
  run,
  view: v,
  inline = false,
  threadId,
  onRegisterCard,
  onOpenThread,
  onRunInChat,
}: {
  run: AgentRun;
  view: AgentView;
  inline?: boolean;
  threadId?: string;
  onRegisterCard?: (id: string, node: HTMLElement | null) => void;
  onOpenThread?: (threadId: string) => void;
  onRunInChat?: (task: AgentTask, run?: AgentRun) => void;
}) {
  const { t: uiText } = useUI();
  const { agents } = useWorkspace();

  const task =
    run.profileId === v.profileId
      ? v.tasks.find((t) => t.id === run.taskId && t.profileId === v.profileId)
      : undefined;
  const notice = v.notices.find((n) => n.runId === run.id);
  const answer = v.aiRequests.find((q) => q.id === run.aiRequestId)?.answer;
  const typedAnalysis = run.strategy === "analysis-v1" || !!task?.ai?.analysis;
  const paper =
    !!run.paper ||
    !!task?.strategyPlan ||
    run.strategy === "paper-limit-buy-v1";
  const acceptedAnswer =
    typedAnalysis && run.analysis?.state !== "completed" ? undefined : answer;

  const canRunInChat =
    !run.wen &&
    !run.botId &&
    !run.swap &&
    !run.wenMining &&
    !!task &&
    !!onRunInChat;

  return (
    <article
      className="memory-card task-run"
      tabIndex={-1}
      aria-label={`Run ${run.id}`}
      ref={(node) => onRegisterCard?.(run.id, node)}
    >
      <header className="task-result-heading">
        <strong>
          {inline
            ? uiText("Latest result")
            : (task?.name ?? uiText("Task unavailable"))}
        </strong>
        <Badge>
          {paper
            ? uiText("Paper")
            : run.watch
              ? uiText("News watch")
              : run.analysis
                ? uiText("Analysis")
                : run.wenMining
                  ? uiText(
                      miningState(
                        v.wenMining?.recovery?.find(
                          (x) => x.taskId === run.taskId,
                        )?.status ?? run.wenMining.state,
                      ),
                    )
                  : run.swap
                    ? uiText(
                        swapState(
                          v.onchain?.recovery?.find(
                            (x) => x.taskId === run.taskId,
                          )?.status ?? run.swap.state,
                        ),
                      )
                    : run.outcome}
        </Badge>
      </header>
      <time dateTime={new Date(run.at * 1000).toISOString()}>
        {formatDate(run.at)}
      </time>
      <p className="task-result-text">
        {paper
          ? uiText(paperOutcome(run.outcome))
          : run.watch
            ? taskCopy("New matches: {count}", {
                count: run.watch.newIds.length,
              })
            : run.analysis?.state === "skipped"
              ? uiText("Analysis skipped")
              : acceptedAnswer ||
                notice?.text ||
                task?.text ||
                uiText("Saved result")}
      </p>
      {run.paper && <PaperResultFacts result={run.paper} />}
      {run.wenMining && (
        <>
          <WENMiningResultFacts
            result={run.wenMining}
            operation={task?.wenMining?.operation}
            recovery={v.wenMining?.recovery?.find(
              (x) => x.taskId === run.taskId,
            )}
          />
          <div className="row-actions">
            <WENMiningRecoveryActions
              recovery={v.wenMining?.recovery?.find(
                (x) => x.taskId === run.taskId,
              )}
              disabled={agents.busy}
              onAction={(action, id) => {
                void agents.change({ action, wenMining: { id } });
              }}
            />
          </div>
        </>
      )}
      {run.swap && (
        <>
          <SwapResultFacts
            result={run.swap}
            quote={task?.swap?.proposal.quote}
            recovery={v.onchain?.recovery?.find((x) => x.taskId === run.taskId)}
          />
          <div className="row-actions">
            <SwapRecoveryActions
              recovery={v.onchain?.recovery?.find(
                (x) => x.taskId === run.taskId,
              )}
              disabled={agents.busy}
              onAction={(action, id) => {
                void agents.change({ action, swap: { id } });
              }}
            />
          </div>
        </>
      )}
      {run.watch && run.analysis?.state === "completed" && answer && (
        <p className="ai-answer">{answer}</p>
      )}
      {run.watch && run.analysis?.state === "skipped" && (
        <p role="status">
          {uiText(
            run.analysis.reason === "no-new-matches"
              ? "Nothing new to analyze"
              : "Analysis skipped",
          )}
        </p>
      )}
      {run.watch && (
        <ul className="watch-headlines">
          {run.watch.newIds.map((id) => {
            const item = v.newsFetches
              .find((f) => f.id === run.news?.fetchId)
              ?.items?.find((n) => n.id === id);
            return item ? (
              <li key={id}>
                {safeHttpsHref(item.url) ? (
                  <a
                    href={safeHttpsHref(item.url)}
                    target="_blank"
                    rel="noreferrer noopener"
                  >
                    {item.title}
                  </a>
                ) : (
                  <span>{item.title}</span>
                )}
              </li>
            ) : null;
          })}
        </ul>
      )}
      {(canRunInChat || (threadId && onOpenThread)) && (
        <div className="row-actions">
          {threadId && onOpenThread && (
            <button
              type="button"
              className="secondary"
              onClick={() => onOpenThread(threadId)}
            >
              {uiText("Open thread")}
            </button>
          )}
          {canRunInChat && task && (
            <button
              type="button"
              className="text-button"
              onClick={() => onRunInChat(task, run)}
            >
              {uiText("Run in Chat")}
            </button>
          )}
        </div>
      )}
      <details className="task-inspection">
        <summary>{uiText("Run details")}</summary>
        <p>
          {run.strategy}
          {uiText(" · Run ")}
          {run.id}
        </p>
        {run.analysis && (
          <dl className="task-facts">
            <dt>{uiText("Analysis")}</dt>
            <dd>
              {uiText(
                run.analysis.state === "completed"
                  ? "Completed"
                  : "Analysis skipped",
              )}
            </dd>
            {run.analysis.reason && (
              <>
                <dt>{uiText("Reason")}</dt>
                <dd>{run.analysis.reason}</dd>
              </>
            )}
          </dl>
        )}
        {run.paper && (
          <dl className="task-facts">
            <dt>{uiText("Source")}</dt>
            <dd>{run.paper.source}</dd>
            <dt>{uiText("Observed")}</dt>
            <dd>{formatDate(run.paper.observedAt)}</dd>
            <dt>{uiText("Digest")}</dt>
            <dd>
              <code className="account-key">{run.paper.consentDigest}</code>
            </dd>
          </dl>
        )}
        {run.botId && (
          <p>
            {uiText("Quantity ")}
            {run.quantity}
            {uiText(" · Fixed price ")}
            {run.priceCents}
            {uiText(" · Cost")} {run.costCents}
            {uiText(" · Budget before ")}
            {run.budgetBefore}
          </p>
        )}
        {run.wen && (
          <p>
            {uiText("Unsigned paper receipt · ")}
            {run.wen.receiptSha256}
          </p>
        )}
        {run.news && (
          <p className="news-provenance">
            {uiText("News snapshot ")}
            {run.news.fetchId} · {run.news.url}
            {uiText(" · retrieved")} {formatDate(run.news.fetchedAt)} ·{" "}
            {run.news.bodySha256}
          </p>
        )}
      </details>
    </article>
  );
}
