package core

// Durable grants live in the existing encrypted credential collection, outside
// metadata backups. Older credential readers refuse this collection version.
// Reservations are synced before dispatch: a failed metadata commit can consume
// allowance conservatively, but a restart cannot restore spent allowance.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"

	"github.com/fased-ai/fased-app/internal/providersecrets"
)

const taskAccessVaultVersion = 100 // Separate from individual account-envelope versions.

type sealedTaskAccess struct {
	Allowance    TaskAccessAllowance `json:"allowance"`
	VaultDigest  string              `json:"vaultDigest"`
	LastObserved int64               `json:"lastObserved"`
	Nonce        []byte              `json:"nonce"`
	Ciphertext   []byte              `json:"ciphertext"`
}

func envelopeDigest(e aiVaultEnvelope) string {
	b, _ := json.Marshal(e)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func retainedTaskAccess(col aiVaultCollection) []sealedTaskAccess {
	var kept []sealedTaskAccess
	for _, record := range col.TaskAccess {
		for _, env := range col.Accounts {
			if record.Allowance.ConnectionRef == env.Ref && record.VaultDigest == envelopeDigest(env) {
				kept = append(kept, record)
				break
			}
		}
	}
	return kept
}

func validateSealedTaskAccess(col aiVaultCollection) error {
	if len(col.TaskAccess) > aiMaxAccounts || len(col.TaskAccess) > 0 && col.Version != taskAccessVaultVersion {
		return errors.New("invalid protected task access")
	}
	seen := map[string]bool{}
	for _, r := range col.TaskAccess {
		a := r.Allowance
		if seen[a.ConnectionRef] || !validConnectionRef(a.ConnectionRef) || a.ID != taskAccessDigest(a) || !a.ContinueAfterRestart || !a.ContinueOnSignOut || a.BootID == "" || len(a.Tasks) < 1 || len(a.Tasks) > 10 || a.MaxRequests < 1 || a.MaxRequests > 10 || a.UsedRequests < 0 || a.UsedRequests > a.MaxRequests || a.BudgetMicrousd < 0 || a.AllocatedMicrousd < 0 || a.AllocatedMicrousd > a.BudgetMicrousd || a.ExpiresAt <= a.CreatedAt || r.LastObserved < a.CreatedAt || len(r.Nonce) != 12 || len(r.Ciphertext) < 16 || len(r.Ciphertext) > aiVaultMaxSerializedBytes {
			return errors.New("invalid protected task access")
		}
		seen[a.ConnectionRef] = true
		matched := false
		for _, env := range col.Accounts {
			if env.Ref == a.ConnectionRef && r.VaultDigest == envelopeDigest(env) && len(r.Ciphertext) <= aiVaultCiphertextBound(env) {
				matched = true
			}
		}
		if !matched {
			return errors.New("protected task credential changed")
		}
	}
	return nil
}

func (s *Store) taskSecretKey(ctx context.Context, create bool) ([]byte, error) {
	source := s.taskSecrets
	if source == nil {
		source = providersecrets.Native{}
	}
	return source.Key(ctx, s.instanceID, create)
}

// Called under the provider mutex, or during construction before publication.
func (s *Store) probeTaskSecrets(ctx context.Context) bool {
	key, err := s.taskSecretKey(ctx, false)
	defer clear(key)
	s.persistentTaskAccessAvailable = err == nil && len(key) == 32 || errors.Is(err, providersecrets.ErrMissing)
	return s.persistentTaskAccessAvailable
}

func (s *Store) taskAccessAAD(r sealedTaskAccess) []byte {
	b, _ := json.Marshal(struct {
		Domain, Instance, Directory string
		OwnerUID                    int
		Allowance                   TaskAccessAllowance
		VaultDigest                 string
		LastObserved                int64
	}{"fased-provider-task-v1", s.instanceID, s.dir, os.Geteuid(), r.Allowance, r.VaultDigest, r.LastObserved})
	return b
}

func (s *Store) syncTaskVault(col aiVaultCollection) error {
	if err := s.writeAIVaultCollection(col); err != nil {
		return err
	}
	dir, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *Store) persistTaskAccess(ctx context.Context, a TaskAccessAllowance, secret []byte, env aiVaultEnvelope, now int64) error {
	col, err := s.readAIVaultCollection()
	if err != nil {
		return err
	}
	key, err := s.taskSecretKey(ctx, a.UsedRequests == 0)
	if err != nil {
		return errors.New("unlock protected system credentials to allow background tasks")
	}
	defer clear(key)
	index := -1
	for i, r := range col.TaskAccess {
		if r.Allowance.ConnectionRef == a.ConnectionRef {
			index = i
			if a.UsedRequests > 0 {
				plain, e := providersecrets.Open(key, r.Nonce, r.Ciphertext, s.taskAccessAAD(r))
				same := bytes.Equal(plain, secret)
				clear(plain)
				if e != nil || !same || r.Allowance.ID != a.ID || a.UsedRequests != r.Allowance.UsedRequests+1 || a.AllocatedMicrousd < r.Allowance.AllocatedMicrousd || now < r.LastObserved {
					return errors.New("background reservation changed; review access again")
				}
			}
		}
	}
	if index < 0 && a.UsedRequests > 0 {
		return errors.New("background grant was revoked")
	}
	r := sealedTaskAccess{Allowance: a, VaultDigest: envelopeDigest(env), LastObserved: now}
	r.Nonce, r.Ciphertext, err = providersecrets.Seal(key, secret, s.taskAccessAAD(r))
	if err != nil {
		return err
	}
	if index < 0 {
		col.TaskAccess = append(col.TaskAccess, r)
	} else {
		col.TaskAccess[index] = r
	}
	col.Version = taskAccessVaultVersion
	return s.syncTaskVault(col)
}

func (s *Store) removeDurableTaskAccess(ref string) error {
	col, err := s.readAIVaultCollection()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	kept := make([]sealedTaskAccess, 0, len(col.TaskAccess))
	for _, r := range col.TaskAccess {
		if r.Allowance.ConnectionRef != ref {
			kept = append(kept, r)
		}
	}
	if len(kept) == len(col.TaskAccess) {
		return nil
	}
	col.TaskAccess = kept
	return s.syncTaskVault(col)
}

func (s *Store) restoreTaskAccess(ctx context.Context) error {
	for ref, state := range s.provider.accounts {
		if state.durableAccess {
			s.clearAIConnectionLocked(ref)
		}
	}
	col, err := s.readAIVaultCollection()
	if err != nil || len(col.TaskAccess) == 0 {
		return nil
	}
	// Observed expiry/rollback is terminal, even if the OS store is locked.
	// Retire it before restoring any authority so a later clock change cannot
	// resurrect it. A failed durable retirement refuses store startup.
	now := s.now().Unix()
	kept := make([]sealedTaskAccess, 0, len(col.TaskAccess))
	for _, r := range col.TaskAccess {
		a := r.Allowance
		if now >= a.CreatedAt && now >= r.LastObserved && now < a.ExpiresAt && a.UsedRequests < a.MaxRequests {
			kept = append(kept, r)
		}
	}
	if len(kept) != len(col.TaskAccess) {
		col.TaskAccess = kept
		if err = s.syncTaskVault(col); err != nil {
			return errors.New("background permission retirement could not be saved")
		}
	}
	if len(col.TaskAccess) == 0 {
		return nil
	}
	d, _, err := readAgents(ctx, s.db)
	if err != nil {
		return nil
	}
	key, err := s.taskSecretKey(ctx, false)
	if err != nil {
		return nil
	}
	defer clear(key)
	for _, r := range col.TaskAccess {
		a := r.Allowance
		if now < a.CreatedAt || now < r.LastObserved || now >= a.ExpiresAt || a.UsedRequests >= a.MaxRequests || agentProfile(d, a.ProfileID) < 0 || s.pilotTargetLocked(a.ConnectionRef) {
			continue
		}
		valid := true
		for _, q := range d.AIRequests {
			if q.ProfileID == a.ProfileID && (aiHasUsageHold(q) || q.State == "running" || q.State == "unknown") {
				valid = false
			}
		}
		for _, binding := range a.Tasks {
			found := false
			for _, task := range d.Tasks {
				if task.ID == binding.ID && task.ProfileID == a.ProfileID && task.AI != nil && task.AI.Digest == binding.Digest && task.AI.ConnectionRef == a.ConnectionRef && task.AI.Model == a.Model && (task.State == "scheduled" || task.State == "paused") && validateTaskAI(d, task, now) == nil {
					found = true
				}
			}
			if !found {
				valid = false
			}
		}
		if !valid {
			continue
		}
		plain, e := providersecrets.Open(key, r.Nonce, r.Ciphertext, s.taskAccessAAD(r))
		if e != nil {
			continue
		}
		state := s.credentialLocked(a.ConnectionRef)
		digestBytes, _ := hex.DecodeString(r.VaultDigest)
		copy(state.digest[:], digestBytes)
		clear(state.key)
		state.key = append([]byte(nil), plain...)
		clear(plain)
		state.ref = a.ConnectionRef
		state.expires = 0
		state.taskAccess = &a
		state.durableAccess = true
		state.taskAccessLast = now
	}
	return nil
}
