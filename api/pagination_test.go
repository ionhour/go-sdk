package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// pagedAPI serves `pages` pages of one item each for any list route, echoing
// the page number into the item id so order can be asserted.
func pagedAPI(t *testing.T, pages int, item func(id int) string) *fakeAPI {
	t.Helper()
	return newFakeAPI(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if pages == 0 {
			writeJSON(w, http.StatusOK, `{"items":[],"total":0,"page":1,"itemCount":0,"pageCount":0,"limit":100}`)
			return
		}
		writeJSON(w, http.StatusOK, `{"items":[`+item(page)+`],"total":`+strconv.Itoa(pages)+
			`,"page":`+strconv.Itoa(page)+`,"itemCount":1,"pageCount":`+strconv.Itoa(pages)+`,"limit":100}`)
	})
}

func withID(body string, id int) string {
	return strings.Replace(body, `"id":`, `"id":`+strconv.Itoa(id)+`0`, 1)
}

func TestListAllWalksEveryPage(t *testing.T) {
	cases := []struct {
		name string
		body string
		list func(ctx context.Context, c *Client) ([]int, error)
	}{
		{"checks", checkBody, func(ctx context.Context, c *Client) ([]int, error) {
			items, err := c.ListAllChecks(ctx, ChecksListParams{})
			ids := make([]int, len(items))
			for i, it := range items {
				ids[i] = it.Id
			}
			return ids, err
		}},
		{"jobs", jobBody, func(ctx context.Context, c *Client) ([]int, error) {
			items, err := c.ListAllJobs(ctx, JobsListParams{})
			ids := make([]int, len(items))
			for i, it := range items {
				ids[i] = it.Id
			}
			return ids, err
		}},
		{"projects", projectBody, func(ctx context.Context, c *Client) ([]int, error) {
			items, err := c.ListAllProjects(ctx, ProjectsListParams{})
			ids := make([]int, len(items))
			for i, it := range items {
				ids[i] = it.Id
			}
			return ids, err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := pagedAPI(t, 3, func(id int) string { return withID(tc.body, id) })
			c, _ := newTestClient(t, f)
			ids, err := tc.list(context.Background(), c)
			if err != nil {
				t.Fatalf("ListAll: %v", err)
			}
			if len(ids) != 3 || ids[0] == ids[1] || ids[1] == ids[2] {
				t.Fatalf("ids = %v, want 3 distinct items in page order", ids)
			}
			reqs := f.requests()
			if len(reqs) != 3 {
				t.Fatalf("requests = %d, want 3", len(reqs))
			}
			for i, r := range reqs {
				q, _ := url.ParseQuery(r.Query)
				if q.Get("page") != strconv.Itoa(i+1) || q.Get("limit") != "100" {
					t.Fatalf("request %d query = %q, want page=%d limit=100", i, r.Query, i+1)
				}
			}
		})
	}
}

func TestListAllKeepsCallerFiltersWithoutMutatingThem(t *testing.T) {
	f := pagedAPI(t, 2, func(id int) string { return withID(projectBody, id) })
	c, _ := newTestClient(t, f)
	params := ProjectsListParams{Name: ptr("Pay"), Limit: ptr(10)}
	if _, err := c.ListAllProjects(context.Background(), params); err != nil {
		t.Fatalf("ListAllProjects: %v", err)
	}
	if params.Page != nil || *params.Limit != 10 || *params.Name != "Pay" {
		t.Fatalf("caller params mutated: %+v", params)
	}
	for i, r := range f.requests() {
		q, _ := url.ParseQuery(r.Query)
		if q.Get("name") != "Pay" || q.Get("limit") != "10" || q.Get("page") != strconv.Itoa(i+1) {
			t.Fatalf("request %d query = %q", i, r.Query)
		}
	}
}

func TestListAllEmptyCollection(t *testing.T) {
	f := pagedAPI(t, 0, nil)
	c, _ := newTestClient(t, f)
	items, err := c.ListAllJobs(context.Background(), JobsListParams{})
	if err != nil || len(items) != 0 || items == nil {
		t.Fatalf("items = %v err = %v, want an empty non-nil slice", items, err)
	}
	if n := len(f.requests()); n != 1 {
		t.Fatalf("requests = %d, want 1", n)
	}
}

func TestListAllStopsOnTheFirstError(t *testing.T) {
	f := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 1 {
			writeJSON(w, http.StatusOK, `{"items":[`+projectBody+`],"total":3,"page":1,"itemCount":1,"pageCount":3,"limit":100}`)
			return
		}
		writeJSON(w, http.StatusForbidden, errorBody(http.StatusForbidden, "KEY_SCOPE_DENIED", "no"))
	})
	c, _ := newTestClient(t, f)
	items, err := c.ListAllProjects(context.Background(), ProjectsListParams{})
	if !IsForbidden(err) || items != nil {
		t.Fatalf("items = %v err = %v, want nil items and a 403", items, err)
	}
	if n := len(f.requests()); n != 2 {
		t.Fatalf("requests = %d, want 2", n)
	}
}

func TestListAllGeneric(t *testing.T) {
	t.Run("stops at pageCount", func(t *testing.T) {
		var pages []int
		got, err := ListAll(context.Background(), func(_ context.Context, page int) ([]string, int, error) {
			pages = append(pages, page)
			return []string{"p" + strconv.Itoa(page)}, 2, nil
		})
		if err != nil || strings.Join(got, ",") != "p1,p2" || len(pages) != 2 {
			t.Fatalf("got %v err %v pages %v", got, err, pages)
		}
	})
	t.Run("an empty page ends the walk even if pageCount says more", func(t *testing.T) {
		calls := 0
		_, err := ListAll(context.Background(), func(_ context.Context, page int) ([]string, int, error) {
			calls++
			if page == 2 {
				return nil, 1000, nil
			}
			return []string{"x"}, 1000, nil
		})
		if err != nil || calls != 2 {
			t.Fatalf("calls = %d err = %v, want 2 and nil", calls, err)
		}
	})
	t.Run("a runaway pageCount is bounded", func(t *testing.T) {
		calls := 0
		_, err := ListAll(context.Background(), func(_ context.Context, _ int) ([]int, int, error) {
			calls++
			return []int{1}, 1 << 30, nil
		})
		if !errors.Is(err, ErrTooManyPages) || calls != maxListPages {
			t.Fatalf("calls = %d err = %v, want %d and ErrTooManyPages", calls, err, maxListPages)
		}
	})
	t.Run("a cancelled context stops before the next page", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		_, err := ListAll(ctx, func(_ context.Context, _ int) ([]int, int, error) {
			calls++
			cancel()
			return []int{1}, 5, nil
		})
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("calls = %d err = %v", calls, err)
		}
	})
}
