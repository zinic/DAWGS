package translate

import (
	"fmt"

	"github.com/specterops/dawgs/cypher/models"
	"github.com/specterops/dawgs/cypher/models/pgsql"
)

func (s *Translator) translateWith() error {
	currentPart := s.query.CurrentPart()

	if !currentPart.HasProjections() {
		currentPart.Frame.Exported.Clear()
	} else {
		var (
			projectedItems = pgsql.NewIdentifierSet()

			// aggregatedItems contains a set of symbols of projected aggregate functions.
			aggregatedItems = pgsql.NewSymbolTable()

			// groupByItems is a set of symbols (identifiers and compound identifiers) that the query is expected to
			// group by. This is built by exclusion of all aggregated items.
			groupByItems = pgsql.NewSymbolTable()

			// extraProjections collects flat _id projections to append after the main loop, so they
			// are not processed by the loop's switch and do not affect groupByItems calculation.
			extraProjections []*Projection
		)

		for _, projectionItem := range currentPart.projections.Items {
			if err := RewriteFrameBindings(s.scope, projectionItem.SelectItem); err != nil {
				return err
			}
		}

		// If an aggregation function is being used, this invokes an implicit group by of non-function projections
		for _, projectionItem := range currentPart.projections.Items {
			switch typedSelectItem := projectionItem.SelectItem.(type) {
			case pgsql.FunctionCall:
				if aggregatedFunctionSymbols, err := GetAggregatedFunctionParameterSymbols(typedSelectItem); err != nil {
					return err
				} else if !aggregatedFunctionSymbols.IsEmpty() {
					aggregatedItems.AddTable(aggregatedFunctionSymbols)
					continue
				}
			}

			if selectItemSymbols, err := SymbolsFor(projectionItem.SelectItem); err != nil {
				return err
			} else {
				groupByItems.Add(selectItemSymbols.NotIn(aggregatedItems))
			}
		}

		set := s.scope.CurrentFrame().Known().RemoveSet(aggregatedItems.RootIdentifiers())
		if s.query.CurrentPart().quantifierIdentifiers != nil && s.query.CurrentPart().quantifierIdentifiers.Len() > 0 {
			set = set.MergeSet(s.query.CurrentPart().quantifierIdentifiers)
		}
		if projectionConstraint, err := s.treeTranslator.ConsumeConstraintsFromVisibleSet(set); err != nil {
			return err
		} else if err := RewriteFrameBindings(s.scope, projectionConstraint.Expression); err != nil {
			return err
		} else {
			currentPart.projections.Constraints = projectionConstraint.Expression
		}

		for idx, projectionItem := range currentPart.projections.Items {
			switch typedSelectItem := projectionItem.SelectItem.(type) {
			case *pgsql.BinaryExpression:
				return fmt.Errorf("binary expression not supported in with statement")

			case pgsql.CompoundIdentifier:
				return fmt.Errorf("compound identifier not supported in with statement")

			case pgsql.Identifier:
				if binding, isBound := s.scope.Lookup(typedSelectItem); !isBound {
					return fmt.Errorf("unable to lookup identifer %s for with statement", typedSelectItem)
				} else {
					// Track this projected item for scope pruning
					projectedItems.Add(binding.Identifier)

					// Capture previous frame info before materializing.
					prevFrameIdent := binding.LastProjection.Binding.Identifier
					hadFlatID := binding.HasFlatID

					// Create a new projection that maps the identifier
					currentPart.projections.Items[idx] = &Projection{
						SelectItem: pgsql.CompoundIdentifier{
							prevFrameIdent, typedSelectItem,
						},
						Alias: pgsql.AsOptionalIdentifier(binding.Identifier),
					}

					// Assign the frame to the binding's last projection backref
					binding.MaterializedBy(currentPart.Frame)

					// If the binding had a flat _id column projected in the previous frame, carry it
					// through using a composite field dereference: (prevFrame.node).id. This form is
					// functionally dependent on the GROUP BY key (the node column) so PostgreSQL
					// accepts it in both aggregated and non-aggregated contexts.
					if hadFlatID {
						flatIDAlias := pgsql.Identifier(string(binding.Identifier) + "_id")
						extraProjections = append(extraProjections, &Projection{
							SelectItem: pgsql.RowColumnReference{
								Identifier: pgsql.CompoundIdentifier{prevFrameIdent, binding.Identifier},
								Column:     pgsql.ColumnID,
							},
							Alias: models.OptionalValue(flatIDAlias),
						})
						// Keep HasFlatID = true so downstream frames continue using the flat-id channel.
					} else {
						binding.HasFlatID = false
					}

					// Reveal and export the identifier in the current multipart query part's frame
					currentPart.Frame.Reveal(binding.Identifier)
					currentPart.Frame.Export(binding.Identifier)
				}

			default:
				// If this is not an identifier then check if the alias is specified. If the alias is specified, this
				// is a pure export (left-hand side is some other expression) and a new bound identifier is being
				// introduced.
				if projectionItem.Alias.Set {
					if binding, isBound := s.scope.AliasedLookup(projectionItem.Alias.Value); !isBound {
						return fmt.Errorf("unable to lookup alias %s for with statement", projectionItem.Alias.Value)
					} else {
						// Track this projected item for scope pruning
						projectedItems.Add(binding.Identifier)

						// Assign the frame to the binding's last projection backref
						binding.LastProjection = currentPart.Frame

						// Reveal and export the identifier in the current multipart query part's frame
						currentPart.Frame.Reveal(binding.Identifier)
						currentPart.Frame.Export(binding.Identifier)

						// Rewrite this projection's alias to use the internal binding
						projectionItem.Alias.Value = binding.Identifier
					}
				}
			}
		}

		// Append flat _id projections collected during the loop. They must be appended after the loop
		// so they do not affect groupByItems construction or trigger the loop's switch statement.
		currentPart.projections.Items = append(currentPart.projections.Items, extraProjections...)

		if !aggregatedItems.IsEmpty() {
			groupByItems.EachIdentifier(func(next pgsql.Identifier) bool {
				currentPart.projections.GroupBy = append(currentPart.projections.GroupBy, next)
				return true
			})

			groupByItems.EachCompoundIdentifier(func(next pgsql.CompoundIdentifier) bool {
				currentPart.projections.GroupBy = append(currentPart.projections.GroupBy, next)
				return true
			})
		}

		if err := s.scope.PruneDefinitions(projectedItems); err != nil {
			return err
		}

		// Prune scope to only what's being exported by the with statement
		currentPart.Frame.Visible = projectedItems.Copy()
		currentPart.Frame.Exported = projectedItems.Copy()
	}

	return nil
}
