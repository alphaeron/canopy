package ghplatform

import "github.com/google/go-github/v93/github"

// perPage is the largest page GitHub serves for the listings the platform
// reads, so a long listing costs as few calls as possible.
const perPage = 100

// paginate calls list for every page of a GitHub listing and returns all
// items. opts is the listing's page cursor: list must read it, and paginate
// advances it to each next page.
func paginate[T any](opts *github.ListOptions, list func() ([]T, *github.Response, error)) ([]T, error) {
	var all []T
	for {
		page, resp, err := list()
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if resp.NextPage == 0 {
			return all, nil
		}
		opts.Page = resp.NextPage
	}
}
