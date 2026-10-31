import { useEffect, useState } from "react";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
import {
  readApplicationUpdates,
  type ApplicationUpdateView,
} from "../application-updates";
export function ApplicationUpdateNotice({ readEpoch }: { readEpoch: number }) {
  const core = useCore();
  const { t } = useUI();
  const [view, setView] = useState<ApplicationUpdateView | null>(null);
  useEffect(() => {
    let current = true;
    if (core.mode === "ready")
      void core
        .request("updates/application")
        .then((v) => {
          if (current) setView(readApplicationUpdates(v));
        })
        .catch(() => {
          if (current) setView(null);
        });
    return () => {
      current = false;
    };
  }, [core.mode, core.request]);
  useEffect(() => {
    if (!readEpoch || !view?.notice || view.notice.read) return;
    let current = true;
    void core
      .request("updates/application", "POST", {
        action: "acknowledge",
        revision: view.revision,
        policyRevision: view.policyRevision,
      })
      .then((v) => {
        if (current) setView(readApplicationUpdates(v));
      })
      .catch(() => {});
    return () => {
      current = false;
    };
  }, [readEpoch, view, core.request]);
  if (!view?.notice) return null;
  return (
    <article className={view.notice.read ? "read" : ""}>
      <span className="dot" />
      <div>
        <strong>{t(view.notice.title)}</strong>
        <p>
          <a href="#settings">{t("Updates")}</a>
        </p>
      </div>
    </article>
  );
}
