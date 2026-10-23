package ghplatform

import "iter"

// perPage is the largest page GitHub serves for the listings the platform
// reads, so a long listing costs as few calls as possible.
const perPage = 100

// collect gathers every item of a go-github listing iterator (the *Iter
// methods, which request each page in turn), or its first error.
func collect[T any](items iter.Seq2[T, error]) ([]T, error) {
	var all []T
	for item, err := range items {
		if err != nil {
			return nil, err
		}
		all = append(all, item)
	}
	return all, nil
}
