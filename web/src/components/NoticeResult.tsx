import { useWorkspace } from "../workspace";
import { useUI } from "../ui-preferences";
import { ArrowRight } from "../icons";

export function NoticeResult({ noticeId }: { noticeId: string }) {
  const { agents, setFocusedTask, setNoticesOpen, navigate } = useWorkspace();
  const { t } = useUI();
  const notice = agents.view?.notices.find((n) => n.id === noticeId);
  const run = agents.view?.runs.find((r) => r.id === notice?.runId);
  if (!run) return null;
  return (
    <button
      className="text-button notice-result"
      onClick={() => {
        setFocusedTask(run.taskId);
        setNoticesOpen(false);
        navigate("agents/tasks");
      }}
    >
      {t("Result")}
      <ArrowRight size={14} />
    </button>
  );
}
