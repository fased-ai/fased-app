import { useWorkspace } from "../workspace";
import { useUI } from "../ui-preferences";
import { ArrowRight } from "../icons";

export function NoticeResult({ noticeId }: { noticeId: string }) {
  const {
    agents,
    core,
    setFocusedTask,
    setFocusedRun,
    setNoticesOpen,
    navigate,
  } = useWorkspace();
  const { t } = useUI();
  const notice = agents.view?.notices.find((n) => n.id === noticeId);
  const profileId = agents.view?.profileId;
  const run = agents.view?.runs.find(
    (r) => r.id === notice?.runId && r.profileId === profileId,
  );
  if (!run || !profileId || notice?.profileId !== profileId) return null;
  return (
    <button
      className="text-button notice-result"
      onClick={() => {
        setFocusedTask("");
        setFocusedRun({ instanceId: core.instanceId, profileId, id: run.id });
        setNoticesOpen(false);
        navigate("agents/tasks");
      }}
    >
      {t("Result")}
      <ArrowRight size={14} />
    </button>
  );
}
