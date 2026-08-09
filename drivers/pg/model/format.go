package model

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"

	"github.com/specterops/dawgs/graph"
)

const (
	NodeTable = "node"
	EdgeTable = "edge"
)

func partitionTableName(parent string, graphID int32) string {
	return parent + "_" + strconv.FormatInt(int64(graphID), 10)
}

func NodePartitionTableName(graphID int32) string {
	return partitionTableName(NodeTable, graphID)
}

func EdgePartitionTableName(graphID int32) string {
	return partitionTableName(EdgeTable, graphID)
}

// EdgeKindPartitionTableName returns the canonical physical leaf name for an
// edge kind. Kind IDs are signed smallints, so negative IDs intentionally
// retain their minus sign in the relation name.
func EdgeKindPartitionTableName(graphID int32, kindID int16) string {
	return EdgePartitionTableName(graphID) + "_" + strconv.FormatInt(int64(kindID), 10)
}

func IndexName(table string, index graph.Index) string {
	return managedName("dawgs_idx_", table, index.Field, index.Type.String())
}

func ConstraintName(table string, constraint graph.Constraint) string {
	return managedName("dawgs_uq_", table, constraint.Field, constraint.Type.String())
}

func managedName(prefix string, components ...string) string {
	digest := sha256.New()
	for _, component := range components {
		digest.Write([]byte{0})
		digest.Write([]byte(component))
	}
	return prefix + hex.EncodeToString(digest.Sum(nil)[:16])
}
