package puzzle

import (
	"math/rand/v2"
	"testing"

	"github.com/hjordan6/trivial/internal/content"
)

func TestWeightedTopicOrderFavorsHigherWeights(t *testing.T) {
	topics := []content.Topic{
		{Slug: "ordinary", SelectionWeight: 1},
		{Slug: "featured", SelectionWeight: 9},
	}
	featuredFirst := 0
	for seed := uint64(1); seed <= 1000; seed++ {
		rng := rand.New(rand.NewPCG(seed, seed+1))
		ordered := weightedTopicOrder(topics, rng)
		if ordered[0].Slug == "featured" {
			featuredFirst++
		}
		if ordered[0].Slug == ordered[1].Slug {
			t.Fatal("weighted order selected a topic twice")
		}
	}
	if featuredFirst < 850 || featuredFirst > 950 {
		t.Fatalf("weight 9 topic selected first %d/1000 times, want approximately 900", featuredFirst)
	}
}

func TestWeightedTopicOrderIsDeterministic(t *testing.T) {
	topics := []content.Topic{{Slug: "a", SelectionWeight: 1}, {Slug: "b", SelectionWeight: 3}, {Slug: "c", SelectionWeight: 2}}
	first := weightedTopicOrder(topics, rand.New(rand.NewPCG(42, 99)))
	second := weightedTopicOrder(topics, rand.New(rand.NewPCG(42, 99)))
	for i := range first {
		if first[i].Slug != second[i].Slug {
			t.Fatalf("order differs at %d", i)
		}
	}
}
