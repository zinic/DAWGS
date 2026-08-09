package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type receiptRecord struct {
	AttemptID string
	Digest    string
	Facts     json.RawMessage
}

// Receipt regenerates a secret-free report from durable database facts. An
// empty receiptType or attemptID selects the newest matching durable record.
func (s *Engine) Receipt(ctx context.Context, migrationID, receiptType, attemptID string) (Report, error) {
	if migrationID == "" {
		return Report{}, fmt.Errorf("migration receipt scope is required")
	}
	var storedKind, storedType, storedAttempt, digest string
	err := s.Pool.QueryRow(ctx, `select scope_kind,receipt_type,attempt_id,receipt_digest
from public.dawgs_schema_receipt
where scope_id=$1
  and ($2='' or receipt_type=$2) and ($3='' or attempt_id=$3)
order by created_at desc,receipt_type desc,attempt_id desc limit 1`, migrationID, receiptType, attemptID).
		Scan(&storedKind, &storedType, &storedAttempt, &digest)
	if err != nil {
		return Report{}, fmt.Errorf("loading durable operation receipt: %w", err)
	}
	_ = storedKind
	return Report{Version: 1, Action: "receipt", State: storedType, MigrationID: migrationID,
		AttemptID: storedAttempt, ReceiptDigest: digest, CreatedAt: time.Now().UTC()}, nil
}

func canonicalDigest(value any) (string, []byte, error) {
	content, err := json.Marshal(value)
	if err != nil {
		return "", nil, err
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), content, nil
}

func appendReceipt(ctx context.Context, tx pgx.Tx, scopeKind, scopeID, receiptType, attemptID string, facts any, token string) (receiptRecord, error) {
	if scopeKind != "bootstrap" && scopeKind != "migration" {
		return receiptRecord{}, fmt.Errorf("unsupported receipt scope %q", scopeKind)
	}
	if scopeID == "" || receiptType == "" || attemptID == "" {
		return receiptRecord{}, fmt.Errorf("receipt scope, type, and attempt are required")
	}
	digest, content, err := canonicalDigest(facts)
	if err != nil {
		return receiptRecord{}, err
	}
	var tokenDigest any
	if token != "" {
		hash := sha256.Sum256([]byte(token))
		tokenDigest = hex.EncodeToString(hash[:])
	}
	tag, err := tx.Exec(ctx, `insert into public.dawgs_schema_receipt
(scope_kind,scope_id,receipt_type,attempt_id,receipt_version,facts,receipt_digest,confirmation_token_digest)
values($1,$2,$3,$4,1,$5::jsonb,$6,$7) on conflict(scope_kind,scope_id,receipt_type,attempt_id) do nothing`,
		scopeKind, scopeID, receiptType, attemptID, content, digest, tokenDigest)
	if err != nil {
		return receiptRecord{}, err
	}
	if tag.RowsAffected() == 0 {
		var priorDigest string
		var priorFacts []byte
		if err := tx.QueryRow(ctx, `select receipt_digest,facts::text from public.dawgs_schema_receipt
where scope_kind=$1 and scope_id=$2 and receipt_type=$3 and attempt_id=$4`,
			scopeKind, scopeID, receiptType, attemptID).Scan(&priorDigest, &priorFacts); err != nil {
			return receiptRecord{}, err
		}
		if priorDigest != digest {
			return receiptRecord{}, fmt.Errorf("receipt attempt %q already records different facts", attemptID)
		}
		return receiptRecord{AttemptID: attemptID, Digest: priorDigest, Facts: priorFacts}, nil
	}
	return receiptRecord{AttemptID: attemptID, Digest: digest, Facts: content}, nil
}
