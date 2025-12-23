package generated

import (
	"backend-gateway-service/graph/model"
	"context"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
)

func (ec *executionContext) unmarshalInputTimestamp(ctx context.Context, v any) (time.Time, error) {
	res, err := model.UnmarshalTimestamp(v)
	return res, graphql.ErrorOnPath(ctx, err)
}

func (ec *executionContext) _Timestamp(ctx context.Context, sel ast.SelectionSet, v *time.Time) graphql.Marshaler {
	if v == nil {
		if !graphql.HasFieldError(ctx, graphql.GetFieldContext(ctx)) {
			graphql.AddErrorf(ctx, "the requested element is null which the schema does not allow")
		}
		return graphql.Null
	}
	res := model.MarshalTimestamp(*v)
	if res == graphql.Null {
		if !graphql.HasFieldError(ctx, graphql.GetFieldContext(ctx)) {
			graphql.AddErrorf(ctx, "the requested element is null which the schema does not allow")
		}
	}
	return res
}
