package model

import (
	"time"

	"github.com/99designs/gqlgen/graphql"
)

func MarshalTimestamp(t time.Time) graphql.Marshaler {
	return graphql.MarshalTime(t)
}

func UnmarshalTimestamp(v any) (time.Time, error) {
	return graphql.UnmarshalTime(v)
}
