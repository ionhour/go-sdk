package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/oapi-codegen/nullable"
)

const (
	projectBody = `{"id":7,"name":"Payments","description":null,"createdAt":"2026-10-11T12:00:00.000Z","updatedAt":"2026-10-11T12:00:00.000Z"}`
	jobBody     = `{"id":456,"slug":"nightly-backup","name":"Nightly Backup","projectId":7,"intervalSeconds":86400,"graceSeconds":600,` +
		`"muteNotifications":false,"downSeverity":"P1","lateSeverity":"P3","paused":false,"status":"NEW","pingToken":null,"pingUrl":null,` +
		`"createdAt":"2026-10-11T12:00:00.000Z","updatedAt":"2026-10-11T12:00:00.000Z"}`
	checkBody = `{"id":101,"slug":"public-api","name":"Uptime - Public API","projectId":7,"checkType":"HTTP","intervalSeconds":300,` +
		`"graceSeconds":30,"muteNotifications":false,"downSeverity":"P1","lateSeverity":"P3","paused":false,"status":"NEW",` +
		`"config":{"url":"https://api.example.com/health","method":"GET","regions":["us-east-1"],"customHeaders":null,` +
		`"expectedStatusMin":200,"expectedStatusMax":399,"failAfterConsecutive":3,"resolveAfterConsecutiveSuccess":2,"timeoutMs":8000,` +
		`"followRedirects":true,"maxRedirects":3,"blockHttpDowngrade":true,"bodyContains":null,"bodyNotContains":null,"latencyWarnMs":null,` +
		`"sslExpiryWarnDays":14,"ipFamily":"AUTO","maxResponseBytes":1048576,"skipTlsVerification":false},` +
		`"createdAt":"2026-10-11T12:00:00.000Z","updatedAt":"2026-10-11T12:00:00.000Z"}`
	regionsBody = `[{"id":"us-east-1","displayName":"US East (Virginia)","flag":"US","enabled":true}]`
)

func page(item string) string {
	return `{"items":[` + item + `],"total":1,"page":2,"itemCount":1,"pageCount":1,"limit":10}`
}

// smokeCase is one generated-client call and the wire request it must make.
type smokeCase struct {
	name       string
	status     int
	respond    string
	call       func(ctx context.Context, c *Client) (status int, decoded any, err error)
	method     string
	rawPath    string // escaped, including the /api prefix
	query      string
	body       string // expected JSON body ("" = no body)
	wantDecode any    // nil = no typed body expected
}

func smokeCases() []smokeCase {
	return append(append(append(checkCases(), jobCases()...), projectCases()...), regionCases()...)
}

func checkCases() []smokeCase {
	return []smokeCase{
		{
			name: "checks create", status: 201, respond: checkBody,
			call: func(ctx context.Context, c *Client) (int, any, error) {
				r, err := c.ChecksCreateWithResponse(ctx, &ChecksCreateParams{IdempotencyKey: ptr("ck-1")}, CreateCheckV1Dto{
					Name: "Uptime - Public API", ProjectId: 7, Slug: nullable.NewNullableWithValue("public-api"),
					IntervalSeconds: ptr(300),
					Config: V1HttpCheckConfigDto{
						Url:           "https://api.example.com/health",
						Method:        nullable.NewNullableWithValue(OutboundHttpMethodGET),
						Regions:       nullable.NewNullableWithValue([]string{"us-east-1"}),
						CustomHeaders: nullable.NewNullableWithValue(map[string]string{"x-env": "prod"}),
					},
				})
				return status(r, err), idOf(r, err, func() int { return r.JSON201.Id }), err
			},
			method: "POST", rawPath: "/api/v1/checks",
			body: `{"name":"Uptime - Public API","projectId":7,"slug":"public-api","intervalSeconds":300,` +
				`"config":{"url":"https://api.example.com/health","method":"GET","regions":["us-east-1"],"customHeaders":{"x-env":"prod"}}}`,
			wantDecode: 101,
		},
		{
			name: "checks get by slug", status: 200, respond: checkBody,
			call: func(ctx context.Context, c *Client) (int, any, error) {
				r, err := c.ChecksGetOneWithResponse(ctx, "slug:public-api")
				return status(r, err), idOf(r, err, func() int { return r.JSON200.Id }), err
			},
			method: "GET", rawPath: "/api/v1/checks/slug:public-api", wantDecode: 101,
		},
		{
			name: "checks list", status: 200, respond: page(checkBody),
			call: func(ctx context.Context, c *Client) (int, any, error) {
				r, err := c.ChecksListWithResponse(ctx, &ChecksListParams{Page: ptr(2), Limit: ptr(10), ProjectId: ptr(7), Status: ptr(CheckStatusOK)})
				return status(r, err), idOf(r, err, func() int { return r.JSON200.Items[0].Id }), err
			},
			method: "GET", rawPath: "/api/v1/checks", query: "limit=10&page=2&projectId=7&status=OK", wantDecode: 101,
		},
		{
			name: "checks patch clears slug and a config field", status: 200, respond: checkBody,
			call: func(ctx context.Context, c *Client) (int, any, error) {
				r, err := c.ChecksUpdateWithResponse(ctx, "101", UpdateCheckV1Dto{
					Name: ptr("Renamed"), Slug: nullable.NewNullNullable[string](),
					Config: &V1HttpCheckConfigPatchDto{BodyContains: nullable.NewNullNullable[string](), TimeoutMs: nullable.NewNullableWithValue(5000)},
				})
				return status(r, err), idOf(r, err, func() int { return r.JSON200.Id }), err
			},
			method: "PATCH", rawPath: "/api/v1/checks/101",
			body:       `{"name":"Renamed","slug":null,"config":{"bodyContains":null,"timeoutMs":5000}}`,
			wantDecode: 101,
		},
		{
			name: "checks delete", status: 204,
			call: func(ctx context.Context, c *Client) (int, any, error) {
				r, err := c.ChecksRemoveWithResponse(ctx, "101")
				return status(r, err), nil, err
			},
			method: "DELETE", rawPath: "/api/v1/checks/101",
		},
		{
			name: "checks pause", status: 200, respond: checkBody,
			call: func(ctx context.Context, c *Client) (int, any, error) {
				r, err := c.ChecksPauseWithResponse(ctx, "101")
				return status(r, err), idOf(r, err, func() int { return r.JSON200.Id }), err
			},
			method: "POST", rawPath: "/api/v1/checks/101/pause", wantDecode: 101,
		},
		{
			name: "checks resume", status: 200, respond: checkBody,
			call: func(ctx context.Context, c *Client) (int, any, error) {
				r, err := c.ChecksResumeWithResponse(ctx, "101")
				return status(r, err), idOf(r, err, func() int { return r.JSON200.Id }), err
			},
			method: "POST", rawPath: "/api/v1/checks/101/resume", wantDecode: 101,
		},
	}
}

func jobCases() []smokeCase {
	return []smokeCase{
		{
			name: "jobs create", status: 201, respond: jobBody,
			call: func(ctx context.Context, c *Client) (int, any, error) {
				r, err := c.JobsCreateWithResponse(ctx, &JobsCreateParams{}, CreateJobV1Dto{
					Name: "Nightly Backup", ProjectId: 7, IntervalSeconds: ptr(86400), Slug: nullable.NewNullableWithValue("nightly-backup"),
				})
				return status(r, err), idOf(r, err, func() int { return r.JSON201.Id }), err
			},
			method: "POST", rawPath: "/api/v1/jobs",
			body:       `{"name":"Nightly Backup","projectId":7,"intervalSeconds":86400,"slug":"nightly-backup"}`,
			wantDecode: 456,
		},
		{
			name: "jobs get", status: 200, respond: jobBody,
			call: func(ctx context.Context, c *Client) (int, any, error) {
				r, err := c.JobsGetOneWithResponse(ctx, "slug:nightly-backup")
				return status(r, err), idOf(r, err, func() int { return r.JSON200.Id }), err
			},
			method: "GET", rawPath: "/api/v1/jobs/slug:nightly-backup", wantDecode: 456,
		},
		{
			name: "jobs list", status: 200, respond: page(jobBody),
			call: func(ctx context.Context, c *Client) (int, any, error) {
				r, err := c.JobsListWithResponse(ctx, &JobsListParams{Slug: ptr("nightly-backup")})
				return status(r, err), idOf(r, err, func() int { return r.JSON200.Items[0].Id }), err
			},
			method: "GET", rawPath: "/api/v1/jobs", query: "slug=nightly-backup", wantDecode: 456,
		},
		{
			name: "jobs patch", status: 200, respond: jobBody,
			call: func(ctx context.Context, c *Client) (int, any, error) {
				r, err := c.JobsUpdateWithResponse(ctx, "456", UpdateJobV1Dto{GraceSeconds: ptr(900), MuteNotifications: ptr(true)})
				return status(r, err), idOf(r, err, func() int { return r.JSON200.Id }), err
			},
			method: "PATCH", rawPath: "/api/v1/jobs/456", body: `{"graceSeconds":900,"muteNotifications":true}`, wantDecode: 456,
		},
		{
			name: "jobs delete", status: 204,
			call: func(ctx context.Context, c *Client) (int, any, error) {
				r, err := c.JobsRemoveWithResponse(ctx, "456")
				return status(r, err), nil, err
			},
			method: "DELETE", rawPath: "/api/v1/jobs/456",
		},
		{
			name: "jobs pause", status: 200, respond: jobBody,
			call: func(ctx context.Context, c *Client) (int, any, error) {
				r, err := c.JobsPauseWithResponse(ctx, "456")
				return status(r, err), idOf(r, err, func() int { return r.JSON200.Id }), err
			},
			method: "POST", rawPath: "/api/v1/jobs/456/pause", wantDecode: 456,
		},
	}
}

func projectCases() []smokeCase {
	return []smokeCase{
		{
			name: "projects create", status: 201, respond: projectBody,
			call: func(ctx context.Context, c *Client) (int, any, error) {
				r, err := c.ProjectsCreateWithResponse(ctx, &ProjectsCreateParams{}, CreateProjectV1Dto{Name: "Payments"})
				return status(r, err), idOf(r, err, func() int { return r.JSON201.Id }), err
			},
			method: "POST", rawPath: "/api/v1/projects", body: `{"name":"Payments"}`, wantDecode: 7,
		},
		{
			name: "projects get", status: 200, respond: projectBody,
			call: func(ctx context.Context, c *Client) (int, any, error) {
				r, err := c.ProjectsGetOneWithResponse(ctx, "7")
				return status(r, err), idOf(r, err, func() int { return r.JSON200.Id }), err
			},
			method: "GET", rawPath: "/api/v1/projects/7", wantDecode: 7,
		},
		{
			name: "projects list", status: 200, respond: page(projectBody),
			call: func(ctx context.Context, c *Client) (int, any, error) {
				r, err := c.ProjectsListWithResponse(ctx, &ProjectsListParams{Name: ptr("Pay ments")})
				return status(r, err), idOf(r, err, func() int { return r.JSON200.Items[0].Id }), err
			},
			method: "GET", rawPath: "/api/v1/projects", query: "name=Pay+ments", wantDecode: 7,
		},
		{
			name: "projects patch clears description", status: 200, respond: projectBody,
			call: func(ctx context.Context, c *Client) (int, any, error) {
				r, err := c.ProjectsUpdateWithResponse(ctx, "7", UpdateProjectV1Dto{Description: nullable.NewNullNullable[string]()})
				return status(r, err), idOf(r, err, func() int { return r.JSON200.Id }), err
			},
			method: "PATCH", rawPath: "/api/v1/projects/7", body: `{"description":null}`, wantDecode: 7,
		},
		{
			name: "projects delete", status: 204,
			call: func(ctx context.Context, c *Client) (int, any, error) {
				r, err := c.ProjectsRemoveWithResponse(ctx, "7")
				return status(r, err), nil, err
			},
			method: "DELETE", rawPath: "/api/v1/projects/7",
		},
	}
}

func regionCases() []smokeCase {
	return []smokeCase{{
		name: "regions list", status: 200, respond: regionsBody,
		call: func(ctx context.Context, c *Client) (int, any, error) {
			r, err := c.RegionsListWithResponse(ctx)
			if err != nil {
				return 0, nil, err
			}
			return r.StatusCode(), (*r.JSON200)[0].Id, nil
		},
		method: "GET", rawPath: "/api/v1/regions", wantDecode: "us-east-1",
	}}
}

// statusCoder is satisfied by every generated *XxxResponse.
type statusCoder interface{ StatusCode() int }

func status(r statusCoder, err error) int {
	if err != nil || reflect.ValueOf(r).IsNil() {
		return 0
	}
	return r.StatusCode()
}

func idOf(r any, err error, get func() int) any {
	if err != nil || reflect.ValueOf(r).IsNil() {
		return nil
	}
	defer func() { _ = recover() }() // nil JSON200/JSON201 -> nil decode, reported by the caller
	return get()
}

func TestGeneratedClientSmoke(t *testing.T) {
	var seen []recordedRequest
	for _, tc := range smokeCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
				if tc.respond == "" {
					w.WriteHeader(tc.status)
					return
				}
				writeJSON(w, tc.status, tc.respond)
			})
			c, _ := newTestClient(t, f)
			gotStatus, decoded, err := tc.call(context.Background(), c)
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if gotStatus != tc.status {
				t.Fatalf("status = %d, want %d", gotStatus, tc.status)
			}
			if tc.wantDecode != nil && decoded != tc.wantDecode {
				t.Fatalf("decoded = %v, want %v", decoded, tc.wantDecode)
			}
			reqs := f.requests()
			if len(reqs) != 1 {
				t.Fatalf("requests = %d, want 1", len(reqs))
			}
			r := reqs[0]
			if r.Method != tc.method || r.RawPath != tc.rawPath || r.Query != tc.query {
				t.Fatalf("request = %s %s?%s, want %s %s?%s", r.Method, r.RawPath, r.Query, tc.method, tc.rawPath, tc.query)
			}
			if r.Header.Get("Authorization") != "Bearer "+testKey {
				t.Fatalf("missing bearer on %s", tc.name)
			}
			assertJSONBody(t, r, tc.body)
			seen = append(seen, r)
		})
	}
	t.Run("every request matches the OpenAPI document", func(t *testing.T) {
		assertRequestsInDocument(t, seen)
	})
}

func assertJSONBody(t *testing.T, r recordedRequest, want string) {
	t.Helper()
	if want == "" {
		if len(r.Body) != 0 {
			t.Fatalf("unexpected body %s", r.Body)
		}
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	var got, exp any
	if err := json.Unmarshal(r.Body, &got); err != nil {
		t.Fatalf("body is not JSON: %s", r.Body)
	}
	if err := json.Unmarshal([]byte(want), &exp); err != nil {
		t.Fatalf("bad expectation: %v", err)
	}
	if !reflect.DeepEqual(got, exp) {
		t.Fatalf("body = %s\nwant   %s", r.Body, want)
	}
}

// documentPath is the committed v1 document, relative to this module. Inside
// the monorepo (detected by monorepoMarker) a missing document is a failure;
// the check is skipped only outside it (e.g. in the public mirror).
const (
	documentPath   = "../../../apps/api-gateway/src/app/openapi/ionhour-v1.json"
	monorepoMarker = "../../../nx.json"
)

type openAPIDoc struct {
	Paths map[string]map[string]struct {
		OperationID string `json:"operationId"`
		RequestBody *struct {
			Content map[string]struct {
				Schema struct {
					Ref string `json:"$ref"`
				} `json:"schema"`
			} `json:"content"`
		} `json:"requestBody"`
	} `json:"paths"`
	Components struct {
		Schemas map[string]struct {
			Required   []string                   `json:"required"`
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"schemas"`
	} `json:"components"`
}

func assertRequestsInDocument(t *testing.T, reqs []recordedRequest) {
	raw, err := os.ReadFile(documentPath)
	if err != nil {
		if _, markerErr := os.Stat(monorepoMarker); markerErr == nil {
			t.Fatalf("inside the monorepo but the OpenAPI document is unreadable: %v", err)
		}
		t.Skipf("outside the monorepo, OpenAPI document not available (%v)", err)
	}
	var doc openAPIDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse document: %v", err)
	}
	for _, r := range reqs {
		path := strings.TrimPrefix(r.Path, "/api")
		tmpl, ok := matchTemplate(doc, path, strings.ToLower(r.Method))
		if !ok {
			t.Fatalf("%s %s is not an operation in the document", r.Method, path)
		}
		op := doc.Paths[tmpl][strings.ToLower(r.Method)]
		if op.RequestBody == nil {
			continue
		}
		ref := op.RequestBody.Content["application/json"].Schema.Ref
		schema := doc.Components.Schemas[strings.TrimPrefix(ref, "#/components/schemas/")]
		var body map[string]json.RawMessage
		if err := json.Unmarshal(r.Body, &body); err != nil {
			t.Fatalf("%s: body is not an object: %s", op.OperationID, r.Body)
		}
		for k := range body {
			if _, ok := schema.Properties[k]; !ok {
				t.Fatalf("%s: body field %q is not in %s", op.OperationID, k, ref)
			}
		}
		for _, k := range schema.Required {
			if _, ok := body[k]; !ok {
				t.Fatalf("%s: required field %q missing from body", op.OperationID, k)
			}
		}
	}
}

func matchTemplate(doc openAPIDoc, path, method string) (string, bool) {
	templates := make([]string, 0, len(doc.Paths))
	for tmpl := range doc.Paths {
		templates = append(templates, tmpl)
	}
	sort.Strings(templates)
	for _, tmpl := range templates {
		pattern := "^" + regexp.MustCompile(`\\\{[^}]+\\\}`).ReplaceAllString(regexp.QuoteMeta(tmpl), `[^/]+`) + "$"
		if _, ok := doc.Paths[tmpl][method]; ok && regexp.MustCompile(pattern).MatchString(path) {
			return tmpl, true
		}
	}
	return "", false
}
