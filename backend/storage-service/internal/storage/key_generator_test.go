package storage

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestObjectKeyGenerator_BuildKeyWithDateTemplate(t *testing.T) {
	generator := NewObjectKeyGenerator(func() time.Time {
		return time.Date(2024, time.March, 15, 12, 0, 0, 0, time.UTC)
	})
	generator.newUUID = func() uuid.UUID {
		return uuid.MustParse("123e4567-e89b-12d3-a456-426614174000")
	}

	key, err := generator.BuildKey("acme", "docs", "doc-42", "docx")
	require.NoError(t, err)
	require.Equal(t, "org/acme/project/docs/year=2024/month=03/day=15/123e4567-e89b-12d3-a456-426614174000/doc-42.docx", key)
}

func TestObjectKeyGenerator_ValidatesInput(t *testing.T) {
	generator := NewObjectKeyGenerator(nil)

	_, err := generator.BuildKey("", "proj", "id", "md")
	require.Error(t, err)

	_, err = generator.BuildKey("org", "", "id", "md")
	require.Error(t, err)

	_, err = generator.BuildKey("org", "proj", "", "md")
	require.Error(t, err)

	_, err = generator.BuildKey("org", "proj", "id", "")
	require.Error(t, err)
}

func TestObjectKeyGenerator_StripsLeadingDot(t *testing.T) {
	generator := NewObjectKeyGenerator(func() time.Time {
		return time.Date(2023, time.April, 1, 0, 0, 0, 0, time.UTC)
	})
	generator.newUUID = func() uuid.UUID {
		return uuid.MustParse("123e4567-e89b-12d3-a456-426614174001")
	}

	key, err := generator.BuildKey("org", "proj", "doc", ".pdf")
	require.NoError(t, err)
	require.Equal(t, "org/org/project/proj/year=2023/month=04/day=01/123e4567-e89b-12d3-a456-426614174001/doc.pdf", key)
}

func TestObjectKeyGenerator_GeneratesUUIDSegment(t *testing.T) {
	generator := NewObjectKeyGenerator(func() time.Time {
		return time.Date(2024, time.January, 10, 0, 0, 0, 0, time.UTC)
	})

	key, err := generator.BuildKey("org", "proj", "doc", "md")
	require.NoError(t, err)

	parts := strings.Split(key, "/")
	require.Len(t, parts, 9)

	uuidSegment := parts[7]
	_, err = uuid.Parse(uuidSegment)
	require.NoError(t, err)
	require.Equal(t, "doc.md", parts[8])
}
