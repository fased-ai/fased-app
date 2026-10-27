import { useEffect, useId, useRef, type ReactNode } from "react";
import { useUI } from "../ui-preferences";
import { X } from "../icons";
import { type Profile } from "../state";
export function Badge({
  children,
  tone = "muted",
}: {
  children: ReactNode;
  tone?: string;
}) {
  return <span className={`badge ${tone}`}>{children}</span>;
}
export function Avatar({
  profile,
  small = false,
}: {
  profile: Profile;
  small?: boolean;
}) {
  return (
    <span className={`avatar ${profile.color} ${small ? "small" : ""}`}>
      {profile.initials}
    </span>
  );
}
export function SectionHead({
  eyebrow,
  title,
  description,
  action,
}: {
  eyebrow?: string;
  title: string;
  description?: string;
  action?: ReactNode;
}) {
  return (
    <div className="section-head">
      <div>
        {eyebrow && <p className="eyebrow">{eyebrow}</p>}
        <h1>{title}</h1>
        {description && <p className="subtle">{description}</p>}
      </div>
      {action}
    </div>
  );
}
export function Empty({
  icon,
  title,
  children,
}: {
  icon: ReactNode;
  title: string;
  children: ReactNode;
}) {
  return (
    <div className="empty-state">
      <div className="empty-icon">{icon}</div>
      <h3>{title}</h3>
      <p>{children}</p>
    </div>
  );
}
export function Dialog({
  title,
  children,
  onClose,
  wide = false,
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
  wide?: boolean;
}) {
  const { t } = useUI();
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  useEffect(() => {
    const el = ref.current;
    el?.showModal();
    return () => {
      el?.close();
    };
  }, []);
  return (
    <dialog
      ref={ref}
      className={wide ? "dialog wide" : "dialog"}
      onCancel={onClose}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
      aria-labelledby={titleId}
    >
      <div className="dialog-heading">
        <h2 id={titleId}>{title}</h2>
        <button
          className="icon-button"
          aria-label={t("Close dialog")}
          onClick={onClose}
        >
          <X size={18} />
        </button>
      </div>
      {children}
    </dialog>
  );
}
export function DemoChart() {
  const { t: uiText } = useUI();
  return (
    <div
      className="chart"
      aria-label={uiText("Illustrative chart, not market data")}
    >
      <div className="chart-grid" />
      <div className="chart-label">
        {uiText("ILLUSTRATIVE DATA · NO MARKET CONNECTION")}
      </div>
      <svg
        viewBox="0 0 800 210"
        role="img"
        aria-label={uiText("Illustrative market price line")}
        preserveAspectRatio="none"
      >
        <defs>
          <linearGradient id="chart-fill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="#b9e765" stopOpacity=".2" />
            <stop offset="100%" stopColor="#b9e765" stopOpacity="0" />
          </linearGradient>
        </defs>
        <path
          d="M0 165 L30 160 L55 170 L75 145 L100 152 L130 122 L150 130 L180 95 L205 107 L230 92 L260 116 L290 80 L320 99 L350 70 L375 85 L400 55 L435 68 L460 55 L490 77 L510 62 L545 79 L570 44 L600 52 L625 31 L660 41 L690 29 L720 45 L750 23 L780 34 L800 17 L800 210 L0 210Z"
          fill="url(#chart-fill)"
        />
        <path
          d="M0 165 L30 160 L55 170 L75 145 L100 152 L130 122 L150 130 L180 95 L205 107 L230 92 L260 116 L290 80 L320 99 L350 70 L375 85 L400 55 L435 68 L460 55 L490 77 L510 62 L545 79 L570 44 L600 52 L625 31 L660 41 L690 29 L720 45 L750 23 L780 34 L800 17"
          fill="none"
          stroke="#b9e765"
          strokeWidth="2"
        />
      </svg>
      <div className="chart-times">
        <span>00:00</span>
        <span>06:00</span>
        <span>12:00</span>
        <span>18:00</span>
        <span>24:00</span>
      </div>
    </div>
  );
}
