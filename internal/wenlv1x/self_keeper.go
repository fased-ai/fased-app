package wenlv1x

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/custody"
	"github.com/fased-ai/fased-app/internal/netguard"
	"github.com/fased-ai/fased-app/internal/onchain"
)

// Start begins the background sovereign Personal Self-Keeper loop (Auto-Reveal, Auto-Claim,
// CloseNonce rent recovery, Yield Autopilot, optional Tier 2 Fallback Crank, and 6h manifest sync).
// Safe and idempotent to call more than once.
func (e *Engine) Start(ctx context.Context) {
	e.keeperMu.Lock()
	if e.keeperStarted || e.closed {
		e.keeperMu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	e.keeperStarted = true
	e.keeperStop = cancel
	e.lastKeeperStatus.Running = true
	interval := e.keeperInterval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	e.keeperWG.Add(1)
	e.keeperMu.Unlock()

	go func() {
		defer e.keeperWG.Done()
		defer func() {
			e.keeperMu.Lock()
			e.keeperStarted = false
			e.lastKeeperStatus.Running = false
			e.keeperMu.Unlock()
		}()

		// Run initial sweep promptly unless in fixture mode with stopped ticker.
		if !e.fixture {
			_, _ = e.TriggerSelfKeeperSweep(runCtx)
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				_, _ = e.TriggerSelfKeeperSweep(runCtx)
			}
		}
	}()
}

// StopKeeper halts the background Self-Keeper loop and waits for exit.
func (e *Engine) StopKeeper() {
	e.keeperMu.Lock()
	cancel := e.keeperStop
	e.keeperStop = nil
	e.keeperMu.Unlock()
	if cancel != nil {
		cancel()
	}
	e.keeperWG.Wait()
}

func (e *Engine) manifestHTTPClient() *http.Client {
	if len(e.manifestURLs) > 0 && e.httpClient != nil {
		return e.httpClient
	}
	return netguard.NewSafeClient(8*time.Second, netguard.Options{})
}

// SyncManifest fetches the WEN signed deployment manifest (with mirror fallback), verifies its
// Ed25519 signature + anti-rollback sequence inside fased-signer, and pins it in custody.
func (e *Engine) SyncManifest(ctx context.Context) (ManifestStatus, error) {
	if e.custody == nil {
		return ManifestStatus{}, custody.ErrWalletNotFound
	}
	urls := e.manifestURLs
	if len(urls) == 0 {
		urls = []string{onchain.LV1XManifestDefaultURL, onchain.LV1XManifestMirrorURL}
	}
	cli := e.manifestHTTPClient()
	var lastErr error
	var verified *onchain.LV1XVerifiedManifest
	for _, rawURL := range urls {
		clean := strings.TrimSpace(rawURL)
		if clean == "" {
			continue
		}
		if len(e.manifestURLs) == 0 {
			if _, vErr := netguard.ValidatePublicHTTPURL(clean, false); vErr != nil {
				lastErr = vErr
				continue
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, clean, nil)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := cli.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, rErr := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		_ = resp.Body.Close()
		if rErr != nil {
			lastErr = rErr
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("manifest endpoint status %d", resp.StatusCode)
			continue
		}
		vm, vErr := e.custody.SetVerifiedMiningManifest(ctx, body)
		if vErr != nil {
			lastErr = vErr
			continue
		}
		verified = &vm
		break
	}

	nowSec := e.now().UTC().Unix()
	e.keeperMu.Lock()
	e.lastManifestSync = nowSec
	if verified != nil {
		e.lastSyncErr = ""
	} else if lastErr != nil {
		e.lastSyncErr = lastErr.Error()
	}
	e.keeperMu.Unlock()

	if verified != nil {
		e.publish(MiningEvent{
			Event:   "manifest.updated",
			Cluster: "mainnet-beta",
		})
	}
	st := e.GetManifestStatus(ctx)
	if verified == nil && lastErr != nil {
		return st, lastErr
	}
	return st, nil
}

// GetManifestStatus returns the custody-pinned LV1X deployment manifest state.
func (e *Engine) GetManifestStatus(ctx context.Context) ManifestStatus {
	out := ManifestStatus{
		DevnetProgramID: custody.EffectiveDevnetProgramID(),
		Status:          "unpinned",
	}
	e.keeperMu.Lock()
	out.LastSyncAt = e.lastManifestSync
	out.LastSyncError = e.lastSyncErr
	e.keeperMu.Unlock()

	if e.custody == nil {
		return out
	}
	vm, err := e.custody.GetVerifiedMiningManifest(ctx)
	if err != nil {
		out.CompatibilityError = err.Error()
		return out
	}
	if vm == nil {
		return out
	}
	out.Pinned = true
	out.KeyID = vm.KeyID
	out.Sequence = vm.SequenceNum
	out.Status = "pinned"
	out.Compatible = vm.Compatible
	out.CompatibilityError = vm.CompatibilityError
	out.IssuedAt = vm.Payload.IssuedAt
	out.ExpiresAt = vm.Payload.ExpiresAt
	out.VerifiedAt = vm.VerifiedAt
	if cl, ok := vm.Payload.Clusters["mainnet-beta"]; ok {
		out.MainnetStatus = cl.Status
		out.MainnetProgramID = cl.ProgramID
	}
	return out
}

func (e *Engine) emitUniqueEvent(key string, ev MiningEvent) {
	nowSec := e.now().UTC().Unix()
	e.keeperMu.Lock()
	if e.emittedEvents == nil {
		e.emittedEvents = make(map[string]int64)
	}
	if prev, ok := e.emittedEvents[key]; ok && nowSec-prev < 120 {
		e.keeperMu.Unlock()
		return
	}
	e.emittedEvents[key] = nowSec
	e.keeperMu.Unlock()
	e.publish(ev)
}

func (e *Engine) reconcileWalletPending(ctx context.Context, walletID string) {
	if e.custody == nil || strings.TrimSpace(walletID) == "" {
		return
	}
	_, ledger, _, err := e.custody.ListActivity(ctx, walletID)
	if err != nil {
		return
	}
	for _, entry := range ledger {
		if entry.State == custody.LedgerBroadcast || entry.State == custody.LedgerUnknown {
			_, _ = e.custody.ReconcileLedgerEntry(ctx, walletID, entry.ID)
		}
	}
}

// SelfKeeperStatus returns the latest telemetry, next Auto-Reveal timestamp, and manifest health.
func (e *Engine) SelfKeeperStatus(ctx context.Context) SelfKeeperStatus {
	e.keeperMu.Lock()
	st := e.lastKeeperStatus
	st.Running = e.keeperStarted
	st.LastManifestSyncAt = e.lastManifestSync
	if st.LastError == "" && e.lastSyncErr != "" {
		st.LastError = e.lastSyncErr
	}
	e.keeperMu.Unlock()

	if st.Cluster == "" {
		st.Cluster = "devnet"
		if e.IsMainnetEnabled(ctx) {
			if w := e.resolveMiningWallet(ctx, "mainnet-beta", ""); w != nil && w.Policy.Mining != nil && w.Policy.Mining.AllowMainnet {
				st.Cluster = "mainnet-beta"
			}
		}
	}
	if w := e.resolveMiningWallet(ctx, st.Cluster, st.WalletID); w != nil {
		st.WalletID = w.ID
		st.WalletPublicKey = w.PublicKey
		if w.Policy.Mining != nil {
			st.AutoClaim = w.Policy.Mining.AutoClaim
			st.AutoStakeWensat = w.Policy.Mining.AutoStakeWensat
			st.AllowFallbackCrank = w.Policy.Mining.AllowFallbackCrank
		}
	}
	if st.NextRevealAt == 0 {
		rt := e.CurrentRound(ctx, st.Cluster)
		var unrevealed int
		if st.WalletID != "" {
			_ = e.db.QueryRowContext(
				ctx,
				`SELECT COUNT(1) FROM lv1x_entries WHERE wallet_id = ? AND round_slot = ? AND kind = 'commit' AND revealed = 0`,
				st.WalletID, int64(rt.RoundSlot),
			).Scan(&unrevealed)
		}
		st.ActiveRoundCommits = unrevealed
		if unrevealed > 0 && rt.EndsAt > 55 {
			st.NextRevealAt = rt.EndsAt - 55
		}
	}
	ms := e.GetManifestStatus(ctx)
	st.ManifestSequence = ms.Sequence
	st.ManifestStatus = nonEmptyStr(ms.MainnetStatus, ms.Status)
	st.ManifestVerifiedAt = ms.VerifiedAt
	st.ManifestCompatible = ms.Compatible
	if st.ManifestError == "" {
		st.ManifestError = ms.CompatibilityError
	}
	return st
}

// TriggerSelfKeeperSweep runs one complete deterministic Self-Keeper pass:
// 1. Lifecycle events (`round.opened`, `round.reveal_window`, `round.settled`, `stake.matured`)
// 2. Auto-Reveal unrevealed active commits in [-55s .. 0s]
// 3. Auto-Claim settled SOL / SAT / cbBTC (w/ solEscape) + CloseNonce(18) rent reclaim
// 4. Yield Autopilot: ActivateStake(22), ClaimStakingReward(25), optional AutoStakeWensat(21)
// 5. Optional Tier 2 Fallback Settlement Crank (>180s stalled user rounds, <=0.005 SOL/day)
// 6. Periodic 6h signed manifest sync
func (e *Engine) TriggerSelfKeeperSweep(ctx context.Context) (SelfKeeperStatus, error) {
	nowSec := e.now().UTC().Unix()
	e.keeperMu.Lock()
	dueManifest := len(e.manifestURLs) > 0 || !e.fixture
	sinceSync := nowSec - e.lastManifestSync
	e.keeperMu.Unlock()
	if dueManifest && (e.lastManifestSync == 0 || sinceSync >= int64(e.manifestSyncInterval.Seconds())) {
		_, _ = e.SyncManifest(ctx)
	}

	cluster := "devnet"
	var wallet *custody.WalletPublic
	if mw := e.resolveMiningWallet(ctx, "mainnet-beta", ""); mw != nil && mw.Policy.Mining != nil && mw.Policy.Mining.Cluster == "mainnet-beta" && mw.Policy.Mining.AllowMainnet && e.IsMainnetEnabled(ctx) {
		wallet = mw
		cluster = "mainnet-beta"
	} else if dw := e.resolveMiningWallet(ctx, "devnet", ""); dw != nil {
		wallet = dw
		cluster = "devnet"
	}

	wid := ""
	if wallet != nil {
		wid = wallet.ID
		if !e.fixture {
			e.reconcileWalletPending(ctx, wid)
		}
	}

	snap := e.readSnapshot(ctx, cluster, wid)
	round := e.CurrentRound(ctx, cluster)
	bind := e.resolveClusterBinding(ctx, cluster)

	// Emit round lifecycle events.
	e.emitUniqueEvent(fmt.Sprintf("opened:%s:%d", cluster, round.RoundSlot), MiningEvent{
		Event:     "round.opened",
		Cluster:   cluster,
		RoundSlot: round.RoundSlot,
	})
	if round.RemainingSec <= 60 {
		e.emitUniqueEvent(fmt.Sprintf("reveal:%s:%d", cluster, round.RoundSlot), MiningEvent{
			Event:     "round.reveal_window",
			Cluster:   cluster,
			RoundSlot: round.RoundSlot,
		})
	}
	if round.RoundSlot > 1 && !e.fixture {
		prevSlot := round.RoundSlot - 1
		if rkAddr, err := onchain.LV1XRoundPDA(bind.ProgramID, bind.LedgerID, prevSlot); err == nil {
			if raws, _, _, fErr := e.fetchAccounts(ctx, cluster, wid, []string{rkAddr}); fErr == nil && len(raws) > 0 && len(raws[0]) == onchain.LV1XSpaceRound {
				if prevRk, dErr := onchain.DecodeLV1XRound(raws[0]); dErr == nil && prevRk.Stage >= 3 {
					e.emitUniqueEvent(fmt.Sprintf("settled:%s:%d", cluster, prevSlot), MiningEvent{
						Event:     "round.settled",
						Cluster:   cluster,
						RoundSlot: prevSlot,
					})
				}
			}
		}
	}
	if snap.StakePos != nil && snap.StakePos.PendingStake > 0 && nowSec >= snap.StakePos.PendingMatureAt {
		e.emitUniqueEvent(fmt.Sprintf("stake_matured:%s:%d", wid, snap.StakePos.PendingMatureAt), MiningEvent{
			Event:   "stake.matured",
			Cluster: cluster,
		})
	}

	var summaryParts []string
	var lastErr string
	var nextReveal int64
	var activeCommits int

	if wallet != nil && wallet.Policy.Mining != nil {
		mPol := wallet.Policy.Mining
		_ = e.db.QueryRowContext(
			ctx,
			`SELECT COUNT(1) FROM lv1x_entries WHERE wallet_id = ? AND round_slot = ? AND kind = 'commit' AND revealed = 0`,
			wid, int64(round.RoundSlot),
		).Scan(&activeCommits)
		if activeCommits > 0 && round.EndsAt > 55 {
			nextReveal = round.EndsAt - 55
		}

		// 1. Auto-Reveal inside the 60s window (starts at <= 55s remaining, retries at <=48s, <=40s).
		if mPol.AutoClaim && round.RemainingSec <= 55 {
			rows, qErr := e.db.QueryContext(ctx, `
SELECT id, shard_id, page, entry_row, nonce, COALESCE(selection, 2), salt_hex
FROM lv1x_entries
WHERE wallet_id = ? AND cluster = ? AND round_slot = ? AND kind = 'commit' AND revealed = 0`,
				wid, cluster, int64(round.RoundSlot),
			)
			if qErr == nil {
				type revJob struct {
					id, salt string
					shard    uint8
					page     uint32
					row, sel uint8
					nonce    uint64
				}
				var jobs []revJob
				for rows.Next() {
					var j revJob
					if rows.Scan(&j.id, &j.shard, &j.page, &j.row, &j.nonce, &j.sel, &j.salt) == nil {
						jobs = append(jobs, j)
					}
				}
				_ = rows.Close()
				revealedCnt := 0
				for _, j := range jobs {
					plainHex, oErr := e.openSalt(wid, cluster, round.RoundSlot, j.nonce, j.salt)
					if oErr != nil || plainHex == "" {
						if oErr != nil {
							lastErr = oErr.Error()
						}
						continue
					}
					_, _, rErr := e.SubmitReveal(ctx, RevealRequest{
						WalletID:   wid,
						Cluster:    cluster,
						RoundSlot:  round.RoundSlot,
						ShardID:    j.shard,
						Page:       j.page,
						EntryRow:   j.row,
						Selection:  j.sel,
						SaltHex:    plainHex,
						Autonomous: true,
					})
					if rErr != nil {
						lastErr = rErr.Error()
					} else {
						revealedCnt++
						_, _ = e.db.ExecContext(ctx, `UPDATE lv1x_entries SET revealed = 1 WHERE id = ?`, j.id)
						if !e.fixture {
							e.reconcileWalletPending(ctx, wid)
						}
					}
				}
				if revealedCnt > 0 {
					summaryParts = append(summaryParts, fmt.Sprintf("revealed %d", revealedCnt))
					activeCommits = max(0, activeCommits-revealedCnt)
				}
			}
		}

		// 2. Auto-Claim (SOL + Vested SAT + cbBTC w/ SolEscape) + CloseNonce(18).
		items, _ := e.ListClaimableItemsForCluster(ctx, cluster, wid)
		pendingClaims := len(items)
		if mPol.AutoClaim && len(items) > 0 {
			claimedCnt := 0
			closedNonces := 0
			for _, it := range items {
				if !it.SolClaimed && it.SolLamports > 0 {
					if _, _, cErr := e.SubmitClaim(ctx, ClaimRequest{
						WalletID: wid, Cluster: cluster, Asset: "sol",
						RoundSlot: it.RoundSlot, ShardID: it.ShardID, Page: it.Page, EntryRow: it.EntryRow, EntriesMask: it.EntriesMask,
						Autonomous: true,
					}); cErr == nil {
						claimedCnt++
						if !e.fixture {
							e.reconcileWalletPending(ctx, wid)
						}
					} else {
						lastErr = cErr.Error()
					}
				}
				if it.VestedAtoms > 0 {
					var satDone int
					_ = e.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sat_claimed), 0) FROM lv1x_entries WHERE wallet_id = ? AND round_slot = ? AND kind = 'commit'`, wid, int64(it.RoundSlot)).Scan(&satDone)
					if satDone == 0 {
						if _, _, cErr := e.SubmitClaim(ctx, ClaimRequest{
							WalletID: wid, Cluster: cluster, Asset: "sat",
							RoundSlot: it.RoundSlot, ShardID: it.ShardID, Page: it.Page, EntryRow: it.EntryRow, EntriesMask: it.EntriesMask, Batch: true, CreateATA: true,
							Autonomous: true,
						}); cErr == nil {
							claimedCnt++
							if !e.fixture {
								e.reconcileWalletPending(ctx, wid)
							}
						} else {
							lastErr = cErr.Error()
						}
					}
				}
				if !it.BtcClaimed && (it.BtcAtoms > 0 || it.SolEscape) {
					if _, _, cErr := e.SubmitClaim(ctx, ClaimRequest{
						WalletID: wid, Cluster: cluster, Asset: "btc",
						RoundSlot: it.RoundSlot, ShardID: it.ShardID, Page: it.Page, EntryRow: it.EntryRow, EntriesMask: it.EntriesMask,
						Batch: true, SolEscape: it.SolEscape, CreateATA: !it.SolEscape,
						Autonomous: true,
					}); cErr == nil {
						claimedCnt++
						if !e.fixture {
							e.reconcileWalletPending(ctx, wid)
						}
					} else {
						lastErr = cErr.Error()
					}
				}
				if !it.NonceClosed && it.Nonce > 0 {
					if _, _, nErr := e.SubmitCloseNonce(ctx, CloseNonceRequest{
						WalletID: wid, Cluster: cluster, RoundSlot: it.RoundSlot, Nonce: it.Nonce, Autonomous: true,
					}); nErr == nil {
						closedNonces++
						if !e.fixture {
							e.reconcileWalletPending(ctx, wid)
						}
					} else {
						lastErr = nErr.Error()
					}
				}
			}
			if claimedCnt > 0 {
				summaryParts = append(summaryParts, fmt.Sprintf("claimed %d", claimedCnt))
			}
			if closedNonces > 0 {
				summaryParts = append(summaryParts, fmt.Sprintf("closed %d nonces", closedNonces))
			}
		}

		// 3. Yield Autopilot (zero-principal ActivateStake + ClaimStakingReward; opt-in AutoStakeWensat).
		if mPol.AutoClaim && snap.StakePos != nil {
			if snap.StakePos.PendingStake > 0 && nowSec >= snap.StakePos.PendingMatureAt {
				if _, _, sErr := e.submitStakingOpWithAuth(ctx, StakeActionRequest{WalletID: wid, Cluster: cluster}, custody.MiningOpActivateStake, "activate_stake", true, ""); sErr == nil {
					summaryParts = append(summaryParts, "activated stake")
					if !e.fixture {
						e.reconcileWalletPending(ctx, wid)
					}
				} else {
					lastErr = sErr.Error()
				}
			}
			if snap.StakePos.UnclaimedBtc > 0 {
				if _, _, sErr := e.submitStakingOpWithAuth(ctx, StakeActionRequest{WalletID: wid, Cluster: cluster, CreateATA: true}, custody.MiningOpClaimStaking, "claim_staking", true, ""); sErr == nil {
					summaryParts = append(summaryParts, "claimed staking btc")
					if !e.fixture {
						e.reconcileWalletPending(ctx, wid)
					}
				} else {
					lastErr = sErr.Error()
				}
			}
		}
		if mPol.AutoStakeWensat && wallet.Policy.ApprovalMode == custody.ApprovalBoundedAuto && snap.WalletSat >= 1.0 {
			if _, _, sErr := e.submitStakingOpWithAuth(ctx, StakeActionRequest{WalletID: wid, Cluster: cluster, AmountSAT: float64(uint64(snap.WalletSat))}, custody.MiningOpStake, "stake", true, ""); sErr == nil {
				summaryParts = append(summaryParts, fmt.Sprintf("auto-staked %d SAT", uint64(snap.WalletSat)))
				if !e.fixture {
					e.reconcileWalletPending(ctx, wid)
				}
			} else {
				lastErr = sErr.Error()
			}
		}

		// 4. Opt-in Tier 2 Fallback Settlement Crank (>180s stalled round where this wallet committed).
		if mPol.AllowFallbackCrank {
			rows, fErr := e.db.QueryContext(ctx, `
SELECT DISTINCT round_slot, shard_id FROM lv1x_entries
WHERE wallet_id = ? AND cluster = ? AND kind = 'commit' AND round_slot < ?
ORDER BY round_slot DESC LIMIT 4`,
				wid, cluster, int64(round.RoundSlot),
			)
			if fErr == nil {
				type stalledCand struct {
					slot  uint64
					shard uint8
				}
				var cands []stalledCand
				for rows.Next() {
					var sc stalledCand
					if rows.Scan(&sc.slot, &sc.shard) == nil {
						cands = append(cands, sc)
					}
				}
				_ = rows.Close()
				for _, sc := range cands {
					rkAddr, err := onchain.LV1XRoundPDA(bind.ProgramID, bind.LedgerID, sc.slot)
					if err != nil {
						continue
					}
					raws, _, _, rErr := e.fetchAccounts(ctx, cluster, wid, []string{rkAddr})
					if rErr != nil || len(raws) == 0 || len(raws[0]) != onchain.LV1XSpaceRound {
						continue
					}
					rk, dErr := onchain.DecodeLV1XRound(raws[0])
					if dErr != nil || rk.Stage >= 3 {
						continue
					}
					expectedEnd := rk.Start + onchain.LV1XRoundIntervalSec
					if expectedEnd <= 0 || nowSec-expectedEnd <= 180 {
						continue
					}
					step := "finish"
					var shardIDs []uint8
					var startPg, pgCnt uint32
					if rk.Stage == 0 {
						step = "begin"
						if rk.ShardCount > 0 && sc.shard < rk.ShardCount {
							shardIDs = []uint8{sc.shard}
						}
					} else if rk.Scanned < rk.TotalPages {
						step = "scan_pages"
						startPg = rk.Scanned
						pgCnt = min(uint32(8), rk.TotalPages-rk.Scanned)
					} else if rk.Allocated < rk.TotalPages {
						step = "allocate_pages"
						startPg = rk.Allocated
						pgCnt = min(uint32(8), rk.TotalPages-rk.Allocated)
					}
					if _, _, crErr := e.SubmitFallbackSettle(ctx, FallbackSettleRequest{
						WalletID:     wid,
						Cluster:      cluster,
						RoundSlot:    sc.slot,
						FallbackStep: step,
						ShardID:      sc.shard,
						ShardIDs:     shardIDs,
						StartPage:    startPg,
						PageCount:    pgCnt,
						Autonomous:   true,
					}); crErr == nil {
						summaryParts = append(summaryParts, fmt.Sprintf("fallback %s #%d", step, sc.slot))
						if !e.fixture {
							e.reconcileWalletPending(ctx, wid)
						}
					} else {
						lastErr = crErr.Error()
					}
					break
				}
			}
		}

		e.keeperMu.Lock()
		e.lastKeeperStatus.PendingClaimsCount = pendingClaims
		e.keeperMu.Unlock()
	}

	summary := "idle"
	if len(summaryParts) > 0 {
		summary = strings.Join(summaryParts, ", ")
	}
	ms := e.GetManifestStatus(ctx)
	if lastErr == "" && ms.LastSyncError != "" {
		lastErr = ms.LastSyncError
	}

	e.keeperMu.Lock()
	st := SelfKeeperStatus{
		Running:            e.keeperStarted,
		Cluster:            cluster,
		WalletID:           wid,
		NextRevealAt:       nextReveal,
		ActiveRoundCommits: activeCommits,
		PendingClaimsCount: e.lastKeeperStatus.PendingClaimsCount,
		LastSweepAt:        nowSec,
		LastSweepSummary:   summary,
		ManifestSequence:   ms.Sequence,
		ManifestStatus:     nonEmptyStr(ms.MainnetStatus, ms.Status),
		ManifestVerifiedAt: ms.VerifiedAt,
		ManifestCompatible: ms.Compatible,
		ManifestError:      ms.CompatibilityError,
		LastManifestSyncAt: e.lastManifestSync,
		LastError:          lastErr,
	}
	if wallet != nil {
		st.WalletPublicKey = wallet.PublicKey
		if wallet.Policy.Mining != nil {
			st.AutoClaim = wallet.Policy.Mining.AutoClaim
			st.AutoStakeWensat = wallet.Policy.Mining.AutoStakeWensat
			st.AllowFallbackCrank = wallet.Policy.Mining.AllowFallbackCrank
		}
	}
	e.lastKeeperStatus = st
	e.keeperMu.Unlock()

	return st, nil
}
