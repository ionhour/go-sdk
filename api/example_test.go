package api_test

import (
	"context"
	"log"

	"github.com/oapi-codegen/nullable"

	"github.com/ionhour/go-sdk/api"
)

// Example mirrors the README quick start. It is compiled (not run) by go test.
func Example() {
	ctx := context.Background()

	c, err := api.NewClient() // IONHOUR_API_KEY, IONHOUR_API_BASE_URL
	if err != nil {
		log.Fatal(err)
	}
	me, err := c.Whoami(ctx)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("workspace %d (%s), %s key", me.WorkspaceId, me.WorkspaceName, me.PrincipalKind)

	key, err := api.NewIdempotencyKey()
	if err != nil {
		log.Fatal(err)
	}
	rsp, err := c.ChecksCreateWithResponse(api.WithIdempotencyKey(ctx, key), &api.ChecksCreateParams{},
		api.CreateCheckV1Dto{
			Name:      "Public API",
			ProjectId: 7,
			Slug:      nullable.NewNullableWithValue("public-api"),
			Config:    api.V1HttpCheckConfigDto{Url: "https://api.example.com/health"},
		})
	if err != nil {
		log.Fatal(err)
	}
	if err := c.CheckResponse(rsp.HTTPResponse, rsp.Body); err != nil {
		log.Fatal(err)
	}
	log.Printf("created check %d", rsp.JSON201.Id)

	got, err := c.ChecksGetOneWithResponse(ctx, "slug:public-api")
	if err != nil {
		log.Fatal(err)
	}
	if err := c.CheckResponse(got.HTTPResponse, got.Body); api.IsNotFound(err) {
		log.Print("gone")
	}

	projects, err := c.ListAllProjects(ctx, api.ProjectsListParams{})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("%d projects", len(projects))

	_, _ = c.ProjectsUpdateWithResponse(ctx, "7", api.UpdateProjectV1Dto{Description: nullable.NewNullNullable[string]()})
}
