package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AgentSuppression is one recipient block scoped to one sending agent.
type AgentSuppression struct {
	AgentEmail string    `json:"agent_email"`
	Address    string    `json:"address"`
	Reason     string    `json:"reason,omitempty"`
	Source     string    `json:"source"`
	CreatedAt  time.Time `json:"created_at"`
}

// AgentSuppressionTxHook runs in the insertion transaction after a new row is
// created and before commit. It is not called for an existing row.
type AgentSuppressionTxHook func(context.Context, pgx.Tx, AgentSuppression) error

// UnsubscribeScope is the exact account, sending agent, and recipient bound to
// an opaque managed-unsubscribe token.
type UnsubscribeScope struct {
	UserID  string
	AgentID string
	Address string
}

// AddAgentSuppression idempotently adds an agent-scoped recipient block.
func (s *Store) AddAgentSuppression(ctx context.Context, userID, agentID, address, reason, source string, onAdded AgentSuppressionTxHook) (sp AgentSuppression, added bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return sp, false, err
	}
	defer tx.Rollback(ctx)

	address = NormalizeEmail(address)
	err = tx.QueryRow(ctx,
		`INSERT INTO agent_suppressions (id, user_id, agent_id, address, reason, source)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (user_id, agent_id, address) DO NOTHING
		 RETURNING agent_id, address, reason, source, created_at`,
		"asupp_"+generateID(), userID, agentID, address, reason, source,
	).Scan(&sp.AgentEmail, &sp.Address, &sp.Reason, &sp.Source, &sp.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx,
			`SELECT agent_id, address, reason, source, created_at
			   FROM agent_suppressions
			  WHERE user_id = $1 AND agent_id = $2 AND address = $3`,
			userID, agentID, address,
		).Scan(&sp.AgentEmail, &sp.Address, &sp.Reason, &sp.Source, &sp.CreatedAt)
		if err != nil {
			return AgentSuppression{}, false, err
		}
	} else if err != nil {
		return AgentSuppression{}, false, err
	} else {
		added = true
		if onAdded != nil {
			if err := onAdded(ctx, tx, sp); err != nil {
				return AgentSuppression{}, false, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentSuppression{}, false, err
	}
	return sp, added, nil
}

// ListAgentSuppressions returns one newest-first keyset page for an exact
// account and agent.
func (s *Store) ListAgentSuppressions(ctx context.Context, userID, agentID string, limit int, after time.Time, afterAddress string) ([]AgentSuppression, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT agent_id, address, reason, source, created_at
	        FROM agent_suppressions
	       WHERE user_id = $1 AND agent_id = $2`
	args := []any{userID, agentID}
	if !after.IsZero() {
		q += fmt.Sprintf(` AND (created_at < $%d OR (created_at = $%d AND address < $%d))`, len(args)+1, len(args)+1, len(args)+2)
		args = append(args, after, NormalizeEmail(afterAddress))
	}
	q += fmt.Sprintf(` ORDER BY created_at DESC, address DESC LIMIT $%d`, len(args)+1)
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentSuppression{}
	for rows.Next() {
		var sp AgentSuppression
		if err := rows.Scan(&sp.AgentEmail, &sp.Address, &sp.Reason, &sp.Source, &sp.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// RemoveAgentSuppression deletes only the exact account/agent/address row.
func (s *Store) RemoveAgentSuppression(ctx context.Context, userID, agentID, address string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM agent_suppressions WHERE user_id = $1 AND agent_id = $2 AND address = $3`,
		userID, agentID, NormalizeEmail(address))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// EffectiveSuppressions returns the deduplicated union of account-wide and
// exact-agent recipient blocks present in addresses.
func (s *Store) EffectiveSuppressions(ctx context.Context, userID, agentID string, addresses []string) ([]string, error) {
	if len(addresses) == 0 {
		return nil, nil
	}
	normalized := make([]string, 0, len(addresses))
	for _, address := range addresses {
		normalized = append(normalized, NormalizeEmail(address))
	}
	rows, err := s.pool.Query(ctx,
		`SELECT address FROM suppressions
		  WHERE user_id = $1 AND address = ANY($2)
		 UNION
		 SELECT address FROM agent_suppressions
		  WHERE user_id = $1 AND agent_id = $3 AND address = ANY($2)`,
		userID, normalized, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var address string
		if err := rows.Scan(&address); err != nil {
			return nil, err
		}
		out = append(out, address)
	}
	return out, rows.Err()
}

// PutUnsubscribeToken idempotently records a token hash's exact scope.
func (s *Store) PutUnsubscribeToken(ctx context.Context, tokenHash []byte, userID, agentID, address string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO agent_unsubscribe_tokens (id, user_id, agent_id, address, token_hash)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (token_hash) DO NOTHING`,
		"aut_"+generateID(), userID, agentID, NormalizeEmail(address), tokenHash)
	return err
}

// ResolveUnsubscribeToken resolves a token hash, returning nil for an unknown
// token without exposing the stored hash.
func (s *Store) ResolveUnsubscribeToken(ctx context.Context, tokenHash []byte) (*UnsubscribeScope, error) {
	var scope UnsubscribeScope
	err := s.pool.QueryRow(ctx,
		`SELECT user_id, agent_id, address FROM agent_unsubscribe_tokens WHERE token_hash = $1`, tokenHash,
	).Scan(&scope.UserID, &scope.AgentID, &scope.Address)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &scope, nil
}
