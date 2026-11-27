import { useState } from "react";
import { Play, X } from "../icons";
import type { TaskLibraries, TaskTemplate } from "../tasks-v2";
import { useUI } from "../ui-preferences";

type LibraryTab = "templates" | "personas" | "skills";

export function TaskLibraryDrawer({
  libraries,
  busyTemplateId,
  onInstantiate,
  onClose,
}: {
  libraries: TaskLibraries | null;
  busyTemplateId: string;
  onInstantiate: (tpl: TaskTemplate) => Promise<void>;
  onClose: () => void;
}) {
  const { t } = useUI();
  const [tab, setTab] = useState<LibraryTab>("templates");

  const templates = libraries?.templates ?? [];
  const personas = libraries?.personas ?? [];
  const skills = libraries?.skills ?? [];

  return (
    <div className="wt-drawer-backdrop" onClick={onClose}>
      <aside
        className="wt-library-drawer"
        aria-label={t("Task libraries")}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="wt-drawer-head">
          <h2>{t("Task Libraries")}</h2>
          <button
            type="button"
            className="f-icon-btn"
            aria-label={t("Close")}
            onClick={onClose}
          >
            <X size={15} aria-hidden="true" />
          </button>
        </div>

        <div className="wt-tabs-underline wt-drawer-tabs" role="tablist">
          <button
            type="button"
            role="tab"
            aria-selected={tab === "templates"}
            onClick={() => setTab("templates")}
          >
            {t("Templates")}{" "}
            <span className="wt-tab-count">{templates.length}</span>
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === "personas"}
            onClick={() => setTab("personas")}
          >
            {t("Personas")}{" "}
            <span className="wt-tab-count">{personas.length}</span>
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === "skills"}
            onClick={() => setTab("skills")}
          >
            {t("Skills")} <span className="wt-tab-count">{skills.length}</span>
          </button>
        </div>

        <div className="wt-drawer-body f-table">
          {tab === "templates" &&
            templates.map((tpl) => (
              <div key={tpl.id} className="f-row wt-lib-row">
                <div className="wt-lib-main">
                  <div className="wt-lib-title">
                    <strong>{tpl.name}</strong>
                    <span className="f-pill">{tpl.goalDomain}</span>
                    <span className="f-pill">{tpl.execMode}</span>
                  </div>
                  <p className="wt-lib-desc">{tpl.description}</p>
                </div>
                <button
                  type="button"
                  className="f-icon-btn"
                  aria-label={`${t("Instantiate")}: ${tpl.name}`}
                  title={t("Instantiate draft")}
                  disabled={busyTemplateId === tpl.id}
                  onClick={() => void onInstantiate(tpl)}
                >
                  <Play size={13} aria-hidden="true" />
                </button>
              </div>
            ))}

          {tab === "personas" &&
            personas.map((persona) => (
              <div key={persona.id} className="f-row wt-lib-row">
                <div className="wt-lib-main">
                  <div className="wt-lib-title">
                    <strong>{persona.name}</strong>
                    <span className="f-pill">{persona.riskProfile}</span>
                  </div>
                  <p className="wt-lib-desc">{persona.role}</p>
                </div>
              </div>
            ))}

          {tab === "skills" &&
            skills.map((skill) => (
              <div key={skill.id} className="f-row wt-lib-row">
                <div className="wt-lib-main">
                  <div className="wt-lib-title">
                    <strong>{skill.name}</strong>
                    <span className="f-pill">{skill.goalDomain}</span>
                  </div>
                  <p className="wt-lib-desc">{skill.instructions}</p>
                </div>
              </div>
            ))}
        </div>
      </aside>
    </div>
  );
}
