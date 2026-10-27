import { useUI } from "../ui-preferences";
import {
  ArrowRight,
  Check,
  Clock3,
  Eye,
  Link2,
  LockKeyhole,
  Plus,
  Shield,
  ShieldCheck,
  Wallet,
} from "../icons";
import { useCore } from "../core";
import { AccountManagement } from "./AccountManagement";
import { walletNames, type WalletMode } from "../state";
import { Badge, SectionHead, Empty } from "../components/shared";
import { useWorkspace } from "../workspace";
export function Wallets() {
  const core = useCore();
  return core.mode === "ready" ? <AccountManagement /> : <WalletPreview />;
}
function WalletPreview() {
  const { t: uiText } = useUI();
  const { state, dispatch, setSetupOpen, setActionOpen, tab, navigate } =
    useWorkspace();
  return (
    <div className="wallet-workspace wallet-preview">
      <SectionHead
        title={uiText("Wallets")}
        action={
          <button className="primary" onClick={() => setSetupOpen(true)}>
            <Plus size={16} />
            {uiText(" Explore wallet setup")}
          </button>
        }
      />
      <div className="tabs" aria-label={uiText("Wallet sections")}>
        {["overview", "accounts", "permissions", "security", "activity"].map(
          (t) => (
            <button
              key={t}
              className={tab === t ? "active" : ""}
              aria-current={tab === t ? "page" : undefined}
              onClick={() => navigate(`wallets/${t}`)}
            >
              {t}
            </button>
          ),
        )}
      </div>
      {tab === "overview" && (
        <>
          <div className="info-banner">
            <ShieldCheck size={18} />
            <div>
              <strong>{uiText("Wallet capability previews")}</strong>
              <span>
                {uiText(
                  "Setup records a preference. These previews do not create keys, connect wallets or grant permissions.",
                )}
              </span>
            </div>
            <Badge>{uiText("DESIGN")}</Badge>
          </div>
          <div className="wallet-mode-grid">
            {(["watch", "external", "automation"] as WalletMode[]).map(
              (mode, i) => {
                const icons = [Eye, Link2, LockKeyhole];
                const Icon = icons[i];
                return (
                  <section className="panel mode-card" key={mode}>
                    <span
                      className={`feature-icon ${["blue", "peach", "lime"][i]}`}
                    >
                      <Icon size={22} />
                    </span>
                    <h2>{walletNames[mode]}</h2>
                    <p>
                      {
                        [
                          "Observe public balances and activity. No signing keys or spending authority.",
                          "Approve supported actions in your existing wallet. Owner presence is required.",
                          "A dedicated operating wallet, protected by the signer and your explicit limits.",
                        ][i]
                      }
                    </p>
                    <ul>
                      {[
                        ["Public account reference", "Read-only by design"],
                        ["Keys stay in your wallet", "Manual approvals"],
                        ["Encrypted key storage", "Independent signer policy"],
                      ][i].map((x) => (
                        <li key={x}>
                          <Check size={13} />
                          {x}
                        </li>
                      ))}
                    </ul>
                    <button
                      className="secondary"
                      onClick={() => {
                        dispatch({
                          type: "setup",
                          patch: { step: 1, walletMode: mode },
                        });
                        setSetupOpen(true);
                      }}
                    >
                      {uiText("Preview this mode ")}
                      <ArrowRight size={14} />
                    </button>
                  </section>
                );
              },
            )}
          </div>
          <section className="panel">
            <Empty
              icon={<Wallet size={26} />}
              title={uiText("Your accounts will appear here")}
            >
              {uiText(
                "There is no live wallet connection in this prototype. Your selected setup mode is",
              )}{" "}
              <strong>{walletNames[state.setup.walletMode]}</strong>.
            </Empty>
          </section>
        </>
      )}
      {tab === "accounts" && (
        <section className="panel">
          <div className="panel-heading">
            <h2>{uiText("Account connections")}</h2>
            <Badge>{uiText("NO LIVE ACCOUNTS")}</Badge>
          </div>
          <Empty
            icon={<Link2 size={25} />}
            title={uiText("Choose capability before connecting")}
          >
            {uiText(
              "A public watch address, an external wallet and an automation wallet have different authority. Setup previews never create keys or load balances.",
            )}
          </Empty>
          <div className="inline-detail">
            <span>{uiText("Selected draft")}</span>
            <strong>{walletNames[state.setup.walletMode]}</strong>
            <button className="text-button" onClick={() => setSetupOpen(true)}>
              {uiText("Change ")}
              <ArrowRight size={14} />
            </button>
          </div>
        </section>
      )}
      {tab === "permissions" && (
        <>
          <div className="info-banner">
            <Shield size={18} />
            <div>
              <strong>{uiText("Persona is not permission.")}</strong>
              <span>
                {uiText(
                  "Names, prompts, models and memory cannot expand spending authority.",
                )}
              </span>
            </div>
          </div>
          <section className="panel">
            <div className="panel-heading">
              <h2>{uiText("Example bounded mandate")}</h2>
              <Badge>{uiText("ILLUSTRATIVE · INACTIVE")}</Badge>
            </div>
            <div className="detail-grid">
              <div>
                <small>{uiText("Assigned profile")}</small>
                <strong>{uiText("WEN Operator")}</strong>
              </div>
              <div>
                <small>{uiText("Operation")}</small>
                <strong>{uiText("Supported typed action only")}</strong>
              </div>
              <div>
                <small>{uiText("Per-action ceiling")}</small>
                <strong>{uiText("50 units · example")}</strong>
              </div>
              <div>
                <small>{uiText("Expiry")}</small>
                <strong>{uiText("Owner-selected · not configured")}</strong>
              </div>
              <div>
                <small>{uiText("Policy enforcement")}</small>
                <strong>{uiText("Independent signer")}</strong>
              </div>
              <div>
                <small>{uiText("Grant status")}</small>
                <strong>{uiText("Not created")}</strong>
              </div>
            </div>
            <div className="panel-footer">
              <p>
                {uiText(
                  "Real limits, assets and destinations will be reviewed in the protected owner flow.",
                )}
              </p>
              <button className="secondary" onClick={() => setActionOpen(true)}>
                {uiText("Preview review card ")}
                <ArrowRight size={14} />
              </button>
            </div>
          </section>
        </>
      )}
      {tab === "security" && (
        <>
          <section className="panel">
            <div className="panel-heading">
              <h2>{uiText("Security model")}</h2>
              <Badge>{uiText("PLANNED · NOT IMPLEMENTED")}</Badge>
            </div>
            <div className="security-list">
              {[
                [
                  "Key isolation",
                  "The application and AI never receive private keys.",
                ],
                [
                  "Manual unlock by default",
                  "A signer restart may require owner presence. Unattended restart explicitly trusts the host.",
                ],
                [
                  "Recoverable backups",
                  "Encrypted backup and restore must be tested before live custody is enabled.",
                ],
                [
                  "Original before submission",
                  "Signed originals and reservations survive restart; unknown outcomes remain held.",
                ],
              ].map(([title, text]) => (
                <div key={title}>
                  <ShieldCheck size={18} />
                  <span>
                    <strong>{title}</strong>
                    <p>{text}</p>
                  </span>
                  <Badge>{uiText("DESIGN")}</Badge>
                </div>
              ))}
            </div>
          </section>
          <div className="info-banner amber">
            <LockKeyhole size={18} />
            <div>
              <strong>{uiText("This prototype holds no keys.")}</strong>
              <span>
                {uiText(
                  "No creation, import, export, encryption or signing functionality has been implemented.",
                )}
              </span>
            </div>
          </div>
        </>
      )}
      {tab === "activity" && (
        <section className="panel">
          <Empty
            icon={<Clock3 size={26} />}
            title={uiText("No financial activity")}
          >
            {uiText(
              "Prototype events appear in Activity. No transaction has been prepared, signed or submitted.",
            )}
          </Empty>
        </section>
      )}
    </div>
  );
}
