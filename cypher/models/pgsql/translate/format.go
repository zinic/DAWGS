package translate

import (
	"bytes"
	"context"
	"strings"

	"github.com/specterops/dawgs/cypher/models/cypher"
	cypherFormat "github.com/specterops/dawgs/cypher/models/cypher/format"
	"github.com/specterops/dawgs/cypher/models/pgsql"
	"github.com/specterops/dawgs/cypher/models/pgsql/format"
)

func Translated(translation Result) (string, error) {
	return format.Statement(translation.Statement, format.NewOutputBuilder().WithGraphScope(translation.GraphID))
}

// postgres comments can be terminated by \r, \n, or both per the source:
// https://github.com/postgres/postgres/blob/824d5f6241ea7a0a85c9d2b3d27beb78e42a36ab/src/backend/parser/scan.l#L186-L211
var newlineToCommentReplacer = strings.NewReplacer(
	"\r\n", "\n-- ",
	"\r", "\n-- ",
	"\n", "\n-- ",
)

func FromCypher(ctx context.Context, regularQuery *cypher.RegularQuery, kindMapper pgsql.KindMapper, stripLiterals bool, graphID int32) (format.Formatted, error) {
	result, err := FromCypherResult(ctx, regularQuery, kindMapper, stripLiterals, graphID)
	return result.Formatted, err
}

type FormattedResult struct {
	format.Formatted
	GraphID             int32
	HasMutations        bool
	RequiredEdgeKindIDs []int16
}

func FromCypherResult(ctx context.Context, regularQuery *cypher.RegularQuery, kindMapper pgsql.KindMapper, stripLiterals bool, graphID int32) (FormattedResult, error) {
	var (
		output  = &bytes.Buffer{}
		emitter = cypherFormat.NewCypherEmitter(stripLiterals)
	)

	// 1. write cypher to output

	if err := emitter.Write(regularQuery, output); err != nil {
		return FormattedResult{}, err
	}

	// 2. save copy of cypher and reset output for commented cypher

	raw := strings.TrimSpace(output.String())
	output.Reset()

	// 3. write commented cypher

	output.WriteString("-- ") // opening comment
	if _, err := newlineToCommentReplacer.WriteString(output, raw); err != nil {
		return FormattedResult{}, err
	}

	// 4. continue with SQL

	output.WriteString("\n")

	if translation, err := Translate(ctx, regularQuery, kindMapper, nil, graphID); err != nil {
		return FormattedResult{}, err
	} else if sqlQuery, err := format.Statement(translation.Statement, format.NewOutputBuilder().WithGraphScope(translation.GraphID)); err != nil {
		return FormattedResult{}, err
	} else {
		output.WriteString(sqlQuery)

		return FormattedResult{
			Formatted: format.Formatted{Statement: output.String(), Parameters: translation.Parameters},
			GraphID:   translation.GraphID, HasMutations: translation.HasMutations,
			RequiredEdgeKindIDs: translation.RequiredEdgeKindIDs,
		}, nil
	}
}
