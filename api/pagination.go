package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

const (
	// listPageLimit is the page size ListAll* requests when the caller sets
	// none: the server's maximum (MAX_PAGE_LIMIT in @ionhour/crud).
	listPageLimit = 100

	// maxListPages bounds a walk so a misbehaving pageCount cannot loop forever
	// (1,000,000 items at the maximum page size).
	maxListPages = 10000
)

// ErrTooManyPages is returned by [ListAll] when a listing claims more than
// maxListPages pages.
var ErrTooManyPages = errors.New("ionhour api: list exceeded the page limit")

// PageFetcher fetches one page (1-based) of a v1 list endpoint and returns its
// items and the server's pageCount.
type PageFetcher[T any] func(ctx context.Context, page int) (items []T, pageCount int, err error)

// ListAll walks the flat v1 pagination (`{items, total, page, itemCount,
// pageCount, limit}`) from page 1 and returns every item in order. It stops at
// pageCount, on an empty page, on the first error (returning no partial
// result) or when ctx ends. Use it for any list endpoint; [Client.ListAllChecks],
// [Client.ListAllJobs] and [Client.ListAllProjects] wrap the common ones.
func ListAll[T any](ctx context.Context, fetch PageFetcher[T]) ([]T, error) {
	all := []T{}
	for page := 1; ; page++ {
		if page > maxListPages {
			return nil, fmt.Errorf("%w (%d pages)", ErrTooManyPages, maxListPages)
		}
		items, pageCount, err := fetch(ctx, page)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if len(items) == 0 || page >= pageCount {
			return all, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
}

// ListAllChecks returns every check matching params (Page is ignored; Limit
// defaults to the server maximum). params is copied, never modified.
func (c *Client) ListAllChecks(ctx context.Context, params ChecksListParams) ([]CheckV1Response, error) {
	return ListAll(ctx, func(ctx context.Context, page int) ([]CheckV1Response, int, error) {
		p := params
		p.Page, p.Limit = &page, pageLimit(params.Limit)
		rsp, err := c.ChecksListWithResponse(ctx, &p)
		if err != nil {
			return nil, 0, err
		}
		if err := c.listError(rsp.HTTPResponse, rsp.Body, rsp.JSON200 == nil); err != nil {
			return nil, 0, err
		}
		return rsp.JSON200.Items, rsp.JSON200.PageCount, nil
	})
}

// ListAllJobs returns every job matching params (Page is ignored; Limit
// defaults to the server maximum). params is copied, never modified.
func (c *Client) ListAllJobs(ctx context.Context, params JobsListParams) ([]JobV1Response, error) {
	return ListAll(ctx, func(ctx context.Context, page int) ([]JobV1Response, int, error) {
		p := params
		p.Page, p.Limit = &page, pageLimit(params.Limit)
		rsp, err := c.JobsListWithResponse(ctx, &p)
		if err != nil {
			return nil, 0, err
		}
		if err := c.listError(rsp.HTTPResponse, rsp.Body, rsp.JSON200 == nil); err != nil {
			return nil, 0, err
		}
		return rsp.JSON200.Items, rsp.JSON200.PageCount, nil
	})
}

// ListAllProjects returns every project matching params (Page is ignored;
// Limit defaults to the server maximum). params is copied, never modified.
func (c *Client) ListAllProjects(ctx context.Context, params ProjectsListParams) ([]ProjectV1Response, error) {
	return ListAll(ctx, func(ctx context.Context, page int) ([]ProjectV1Response, int, error) {
		p := params
		p.Page, p.Limit = &page, pageLimit(params.Limit)
		rsp, err := c.ProjectsListWithResponse(ctx, &p)
		if err != nil {
			return nil, 0, err
		}
		if err := c.listError(rsp.HTTPResponse, rsp.Body, rsp.JSON200 == nil); err != nil {
			return nil, 0, err
		}
		return rsp.JSON200.Items, rsp.JSON200.PageCount, nil
	})
}

// listError maps a non-2xx page to *APIError and a 2xx page without a decoded
// body to a descriptive error.
func (c *Client) listError(rsp *http.Response, body []byte, undecoded bool) error {
	if err := c.CheckResponse(rsp, body); err != nil {
		return err
	}
	if undecoded {
		return fmt.Errorf("ionhour api: list page was not a JSON page (status %d)", rsp.StatusCode)
	}
	return nil
}

// pageLimit returns a fresh pointer: the caller's limit, or the server max.
func pageLimit(limit *int) *int {
	n := listPageLimit
	if limit != nil {
		n = *limit
	}
	return &n
}
