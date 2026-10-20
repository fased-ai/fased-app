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
  const { state, dispatch, setSetupOpen, setActionOpen, tab, navigate } =
    useWorkspace();
  return (
    <>
      <SectionHead
        eyebrow="ACCOUNTS & AUTHORITY"
        title="Wallets"
        description="Choose the custody you need. Keep permissions visible."
        action={
          <button className="primary" onClick={() => setSetupOpen(true)}>
            <Plus size={16} /> Explore wallet setup
          </button>
        }
      />
      <div className="tabs" aria-label="Wallet sections">
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
              <strong>One wallet service. Explicit authority.</strong>
              <span>
                Keys belong to the isolated signer. Profiles and models receive
                account references and bounded permissions.
              </span>
            </div>
            <Badge>DESIGN</Badge>
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
                      Preview this mode <ArrowRight size={14} />
                    </button>
                  </section>
                );
              },
            )}
          </div>
          <section className="panel">
            <Empty
              icon={<Wallet size={26} />}
              title="Your accounts will appear here"
            >
              There is no live wallet connection in this prototype. Your
              selected setup mode is{" "}
              <strong>{walletNames[state.setup.walletMode]}</strong>.
            </Empty>
          </section>
        </>
      )}
      {tab === "accounts" && (
        <section className="panel">
          <div className="panel-heading">
            <h2>Account connections</h2>
            <Badge>NO LIVE ACCOUNTS</Badge>
          </div>
          <Empty
            icon={<Link2 size={25} />}
            title="Choose capability before connecting"
          >
            A public watch address, an external wallet and an automation wallet
            have different authority. Setup previews never create keys or load
            balances.
          </Empty>
          <div className="inline-detail">
            <span>Selected draft</span>
            <strong>{walletNames[state.setup.walletMode]}</strong>
            <button className="text-button" onClick={() => setSetupOpen(true)}>
              Change <ArrowRight size={14} />
            </button>
          </div>
        </section>
      )}
      {tab === "permissions" && (
        <>
          <div className="info-banner">
            <Shield size={18} />
            <div>
              <strong>Persona is not permission.</strong>
              <span>
                Names, prompts, models and memory cannot expand spending
                authority.
              </span>
            </div>
          </div>
          <section className="panel">
            <div className="panel-heading">
              <h2>Example bounded mandate</h2>
              <Badge>ILLUSTRATIVE · INACTIVE</Badge>
            </div>
            <div className="detail-grid">
              <div>
                <small>Assigned profile</small>
                <strong>WEN Operator</strong>
              </div>
              <div>
                <small>Operation</small>
                <strong>Supported typed action only</strong>
              </div>
              <div>
                <small>Per-action ceiling</small>
                <strong>50 units · example</strong>
              </div>
              <div>
                <small>Expiry</small>
                <strong>Owner-selected · not configured</strong>
              </div>
              <div>
                <small>Policy enforcement</small>
                <strong>Independent signer</strong>
              </div>
              <div>
                <small>Grant status</small>
                <strong>Not created</strong>
              </div>
            </div>
            <div className="panel-footer">
              <p>
                Real limits, assets and destinations will be reviewed in the
                protected owner flow.
              </p>
              <button className="secondary" onClick={() => setActionOpen(true)}>
                Preview review card <ArrowRight size={14} />
              </button>
            </div>
          </section>
        </>
      )}
      {tab === "security" && (
        <>
          <section className="panel">
            <div className="panel-heading">
              <h2>Security model</h2>
              <Badge>PLANNED · NOT IMPLEMENTED</Badge>
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
                  <Badge>DESIGN</Badge>
                </div>
              ))}
            </div>
          </section>
          <div className="info-banner amber">
            <LockKeyhole size={18} />
            <div>
              <strong>This prototype holds no keys.</strong>
              <span>
                No creation, import, export, encryption or signing functionality
                has been implemented.
              </span>
            </div>
          </div>
        </>
      )}
      {tab === "activity" && (
        <section className="panel">
          <Empty icon={<Clock3 size={26} />} title="No financial activity">
            Prototype events appear in Activity. No transaction has been
            prepared, signed or submitted.
          </Empty>
        </section>
      )}
    </>
  );
}
