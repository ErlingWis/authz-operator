/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package authzproxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	authv1 "my.domain/fga/api/v1"
)

func TestDiscovery(t *testing.T) {
	server := newTestServer(t)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, discoveryPath, nil)

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var body map[string]string
	decodeJSON(t, response.Body, &body)
	if body["access_evaluation_endpoint"] != evaluationPath {
		t.Fatalf("access_evaluation_endpoint = %q, want %q", body["access_evaluation_endpoint"], evaluationPath)
	}
}

func TestAuthZENForwarding(t *testing.T) {
	var forwarded *http.Request
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		forwarded = request
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		}, nil
	})

	server := newTestServer(t,
		stableRelease("store-1", "model-1"),
	).withHTTPClient(&http.Client{Transport: transport}).
		withAPIURL("https://openfga.example.test/base").
		withAPIToken("token-1")

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, evaluationPath+"?trace=true", strings.NewReader(`{"subject":{}}`))
	request.Header.Set("Content-Type", "application/json")

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusAccepted)
	}
	if forwarded == nil {
		t.Fatal("request was not forwarded")
	}
	if forwarded.URL.String() != "https://openfga.example.test/base/stores/store-1/access/v1/evaluation?trace=true" {
		t.Fatalf("forwarded URL = %q", forwarded.URL.String())
	}
	if forwarded.Host != "openfga.example.test" {
		t.Fatalf("forwarded host = %q, want openfga.example.test", forwarded.Host)
	}
	if forwarded.Header.Get(AuthorizationModelHeader) != "model-1" {
		t.Fatalf("model header = %q, want model-1", forwarded.Header.Get(AuthorizationModelHeader))
	}
	if forwarded.Header.Get("Authorization") != "Bearer token-1" {
		t.Fatalf("authorization header = %q, want bearer token", forwarded.Header.Get("Authorization"))
	}
}

func TestAuthZENForwardingWithoutStableRelease(t *testing.T) {
	server := newTestServer(t)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, evaluationPath, nil)

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestResourceTypeAccessSchema(t *testing.T) {
	server := newTestServer(t, documentModule())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, resourceTypesPrefix+"document"+accessSchemaPathSegment, nil)

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var body accessSchemaResponse
	decodeJSON(t, response.Body, &body)
	if body.ResourceType != "document" {
		t.Fatalf("resourceType = %q, want document", body.ResourceType)
	}
	if _, ok := body.Roles["owner"]; !ok {
		t.Fatalf("roles = %#v, want owner role", body.Roles)
	}
	if body.Permissions["view"].AnyOf[0] != "owner" {
		t.Fatalf("view permission = %#v, want owner", body.Permissions["view"])
	}
}

func TestRoleAssignments(t *testing.T) {
	server := newTestServer(t,
		stableRelease("store-1", "model-1"),
		documentModule(),
	).withTupleReader(&fakeTupleReader{tuples: []Tuple{
		{Object: "document:doc-1", Relation: "owner", User: "user:b"},
		{Object: "document:doc-1", Relation: "owner", User: "user:a"},
		{Object: "document:doc-1", Relation: "view", User: "user:c"},
		{Object: "document:doc-2", Relation: "owner", User: "user:d"},
	}})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, roleAssignmentsPath+"?resourceType=document&resourceId=doc-1", nil)

	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var body roleAssignmentsResponse
	decodeJSON(t, response.Body, &body)
	want := []string{"user:a", "user:b"}
	got := body.Assignments["owner"]
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("owner assignments = %#v, want %#v", got, want)
	}
	if _, ok := body.Assignments["view"]; ok {
		t.Fatalf("assignments included permission relation: %#v", body.Assignments)
	}
}

type testServer struct {
	*Server
}

func newTestServer(t *testing.T, objects ...client.Object) testServer {
	t.Helper()
	return testServer{Server: &Server{
		Client:      fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(objects...).Build(),
		APIURL:      "https://openfga.example.test",
		Namespace:   "default",
		ReleaseName: "release",
	}}
}

func (s testServer) withHTTPClient(httpClient *http.Client) testServer {
	s.HTTPClient = httpClient
	return s
}

func (s testServer) withAPIURL(apiURL string) testServer {
	s.APIURL = apiURL
	return s
}

func (s testServer) withAPIToken(token string) testServer {
	s.APIToken = token
	return s
}

func (s testServer) withTupleReader(reader TupleReader) testServer {
	s.TupleReader = reader
	return s
}

type fakeTupleReader struct {
	tuples []Tuple
}

func (r *fakeTupleReader) ReadObjectTuples(_ context.Context, _ authv1.AuthorizationModelReleaseState, _ string) ([]Tuple, error) {
	return r.tuples, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := authv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

func stableRelease(storeID, modelID string) *authv1.AuthorizationModelRelease {
	return &authv1.AuthorizationModelRelease{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "release",
		},
		Status: authv1.AuthorizationModelReleaseStatus{
			Stable: &authv1.AuthorizationModelReleaseState{
				ModelHash:      "hash-1",
				OpenFGAStoreID: storeID,
				OpenFGAModelID: modelID,
				PublishedAt:    metav1.NewTime(time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)),
			},
		},
	}
}

func documentModule() *authv1.AuthorizationModule {
	return &authv1.AuthorizationModule{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "document",
		},
		Spec: authv1.AuthorizationModuleSpec{
			Resource: "document",
			Topology: map[string]authv1.TopologyRelation{
				"parent": {Resources: []string{"folder"}},
			},
			Roles: map[string]authv1.AuthorizationRole{
				"owner": {Subjects: []authv1.AuthorizationSubject{{Type: "user"}}},
			},
			Permissions: map[string]authv1.AuthorizationPermission{
				"view": {AnyOf: []string{"owner"}},
			},
		},
	}
}

func decodeJSON(t *testing.T, reader io.Reader, value any) {
	t.Helper()
	if err := json.NewDecoder(reader).Decode(value); err != nil {
		t.Fatal(err)
	}
}
