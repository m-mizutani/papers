package model_test

import (
	"math/rand"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func TestKeyBetween(t *testing.T) {
	t.Run("empty bounds give a valid key", func(t *testing.T) {
		k := gt.R1(model.KeyBetween("", "")).NoError(t)
		gt.True(t, model.ValidOrderKey(k))
	})

	t.Run("appending keeps increasing order", func(t *testing.T) {
		prev := ""
		for i := 0; i < 500; i++ {
			k := gt.R1(model.KeyBetween(prev, "")).NoError(t)
			gt.True(t, model.ValidOrderKey(k))
			if prev != "" {
				gt.True(t, prev < k)
			}
			prev = k
		}
	})

	t.Run("prepending keeps decreasing order", func(t *testing.T) {
		next := ""
		for i := 0; i < 500; i++ {
			k := gt.R1(model.KeyBetween("", next)).NoError(t)
			gt.True(t, model.ValidOrderKey(k))
			if next != "" {
				gt.True(t, k < next)
			}
			next = k
		}
	})

	t.Run("random insertions stay strictly ordered", func(t *testing.T) {
		rng := rand.New(rand.NewSource(1))
		keys := []string{gt.R1(model.KeyBetween("", "")).NoError(t)}
		for i := 0; i < 2000; i++ {
			pos := rng.Intn(len(keys) + 1)
			lo, hi := "", ""
			if pos > 0 {
				lo = keys[pos-1]
			}
			if pos < len(keys) {
				hi = keys[pos]
			}
			k := gt.R1(model.KeyBetween(lo, hi)).NoError(t)
			gt.True(t, model.ValidOrderKey(k))
			if lo != "" {
				gt.True(t, lo < k)
			}
			if hi != "" {
				gt.True(t, k < hi)
			}
			keys = append(keys[:pos], append([]string{k}, keys[pos:]...)...)
		}
	})

	t.Run("rejects inverted or invalid bounds", func(t *testing.T) {
		gt.R1(model.KeyBetween("b", "a")).Error(t)
		gt.R1(model.KeyBetween("a", "a")).Error(t)
		gt.R1(model.KeyBetween("a0", "")).Error(t)
		gt.R1(model.KeyBetween("", "a!")).Error(t)
	})
}

func TestValidOrderKey(t *testing.T) {
	gt.True(t, model.ValidOrderKey("V"))
	gt.True(t, model.ValidOrderKey("a1"))
	gt.False(t, model.ValidOrderKey(""))
	gt.False(t, model.ValidOrderKey("a0"))
	gt.False(t, model.ValidOrderKey("a-"))
}
