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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	openfgaclient "github.com/openfga/go-sdk/client"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	authv1 "erli.ng/authz-operator/api/v1"
	"erli.ng/authz-operator/internal/openfgaconfig"
)

const (
	DefaultNamespace   = "bridder-system"
	DefaultReleaseName = "bridder-authorization-model"
	DefaultBindAddress = ":8080"

	AuthorizationModelHeader = "Openfga-Authorization-Model-Id"

	discoveryPath      = "/.well-known/authzen-configuration"
	evaluationPath     = "/access/v1/evaluation"
	evaluationsPath    = "/access/v1/evaluations"
	resourceSearchPath = "/access/v1/search/resource"
	subjectSearchPath  = "/access/v1/search/subject"
	actionSearchPath   = "/access/v1/search/action"

	resourceTypesPath       = "/bridder/v1/resource-types"
	resourceTypesPrefix     = "/bridder/v1/resource-types/"
	roleAssignmentsPath     = "/bridder/v1/role-assignments"
	accessSchemaPathSegment = "/access-schema"
)

var (
	errStableReleaseUnavailable = errors.New("stable authorization model is unavailable")
	errResourceTypeNotFound     = errors.New("resource type was not found")
)

// Tuple identifies a relationship tuple returned from OpenFGA.
type Tuple struct {
	User     string
	Relation string
	Object   string
}

// TupleReader reads tuples from the stable OpenFGA store.
type TupleReader interface {
	ReadObjectTuples(ctx context.Context, stable authv1.AuthorizationModelReleaseState, object string) ([]Tuple, error)
}

// Server serves AuthZEN proxy routes and Bridder authorization metadata routes.
type Server struct {
	Client      client.Client
	TupleReader TupleReader
	HTTPClient  *http.Client
	APIURL      string
	APIToken    string
	Namespace   string
	ReleaseName string
}

// OpenFGATupleReader reads tuple assignments through the OpenFGA SDK.
type OpenFGATupleReader struct {
	APIURL   string
	APIToken string
}

func (r *OpenFGATupleReader) ReadObjectTuples(ctx context.Context, stable authv1.AuthorizationModelReleaseState, object string) ([]Tuple, error) {
	sdkClient, err := openfgaclient.NewSdkClient(openfgaconfig.New(r.APIURL, r.APIToken, stable.OpenFGAStoreID))
	if err != nil {
		return nil, err
	}

	var tuples []Tuple
	var continuationToken *string
	for {
		response, err := sdkClient.Read(ctx).
			Options(openfgaclient.ClientReadOptions{
				ContinuationToken: continuationToken,
				StoreId:           &stable.OpenFGAStoreID,
			}).
			Body(openfgaclient.ClientReadRequest{Object: &object}).
			Execute()
		if err != nil {
			return nil, err
		}
		for _, tuple := range response.GetTuples() {
			key := tuple.GetKey()
			tuples = append(tuples, Tuple{
				User:     key.GetUser(),
				Relation: key.GetRelation(),
				Object:   key.GetObject(),
			})
		}
		token := response.GetContinuationToken()
		if token == "" {
			return tuples, nil
		}
		continuationToken = &token
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(discoveryPath, s.handleDiscovery)
	for _, path := range authZENProxyPaths() {
		mux.HandleFunc(path, s.handleAuthZENProxy)
	}
	mux.HandleFunc(resourceTypesPath, s.handleResourceTypes)
	mux.HandleFunc(resourceTypesPrefix, s.handleResourceType)
	mux.HandleFunc(roleAssignmentsPath, s.handleRoleAssignments)
	return mux
}

func (s *Server) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"access_evaluation_endpoint":  evaluationPath,
		"access_evaluations_endpoint": evaluationsPath,
		"search_resource_endpoint":    resourceSearchPath,
		"search_subject_endpoint":     subjectSearchPath,
		"search_action_endpoint":      actionSearchPath,
	})
}

func (s *Server) handleAuthZENProxy(w http.ResponseWriter, r *http.Request) {
	stable, err := s.stableRelease(r.Context())
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}

	upstream, err := s.upstreamURL(stable.OpenFGAStoreID, r.URL)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	request, err := http.NewRequestWithContext(r.Context(), r.Method, upstream.String(), r.Body)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	request.Header = r.Header.Clone()
	request.Header.Set(AuthorizationModelHeader, stable.OpenFGAModelID)
	if s.APIToken != "" {
		request.Header.Set("Authorization", "Bearer "+s.APIToken)
	}
	request.Host = upstream.Host

	response, err := s.httpClient().Do(request)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer func() {
		_ = response.Body.Close()
	}()

	copyHeader(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

func (s *Server) handleResourceTypes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	modules, err := s.modules(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resourceTypes := make([]string, 0, len(modules))
	for _, module := range modules {
		resourceTypes = append(resourceTypes, module.Spec.Resource)
	}
	sort.Strings(resourceTypes)
	writeJSON(w, http.StatusOK, map[string][]string{"resourceTypes": resourceTypes})
}

func (s *Server) handleResourceType(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	resourceType, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, resourceTypesPrefix), accessSchemaPathSegment)
	if !ok || resourceType == "" || strings.Contains(resourceType, "/") {
		http.NotFound(w, r)
		return
	}
	module, err := s.moduleForResource(r.Context(), resourceType)
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, accessSchemaResponse{
		ResourceType: resourceType,
		Topology:     module.Spec.Topology,
		Roles:        module.Spec.Roles,
		Permissions:  module.Spec.Permissions,
	})
}

func (s *Server) handleRoleAssignments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	resourceType := r.URL.Query().Get("resourceType")
	resourceID := r.URL.Query().Get("resourceId")
	if resourceType == "" || resourceID == "" {
		writeError(w, http.StatusBadRequest, "resourceType and resourceId are required")
		return
	}

	module, err := s.moduleForResource(r.Context(), resourceType)
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	stable, err := s.stableRelease(r.Context())
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	tuples, err := s.tupleReader().ReadObjectTuples(r.Context(), *stable, resourceType+":"+resourceID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	roleSet := map[string]struct{}{}
	for role := range module.Spec.Roles {
		roleSet[role] = struct{}{}
	}
	assignments := map[string][]string{}
	for _, tuple := range tuples {
		if tuple.Object != resourceType+":"+resourceID {
			continue
		}
		if _, ok := roleSet[tuple.Relation]; !ok {
			continue
		}
		assignments[tuple.Relation] = append(assignments[tuple.Relation], tuple.User)
	}
	for role := range assignments {
		sort.Strings(assignments[role])
	}

	writeJSON(w, http.StatusOK, roleAssignmentsResponse{
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Assignments:  assignments,
	})
}

func (s *Server) stableRelease(ctx context.Context) (*authv1.AuthorizationModelReleaseState, error) {
	var release authv1.AuthorizationModelRelease
	if err := s.Client.Get(ctx, types.NamespacedName{Namespace: s.namespace(), Name: s.releaseName()}, &release); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, errStableReleaseUnavailable
		}
		return nil, err
	}
	if release.Status.Stable == nil {
		return nil, errStableReleaseUnavailable
	}
	return release.Status.Stable, nil
}

func (s *Server) modules(ctx context.Context) ([]authv1.AuthorizationModule, error) {
	var list authv1.AuthorizationModuleList
	if err := s.Client.List(ctx, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (s *Server) moduleForResource(ctx context.Context, resourceType string) (*authv1.AuthorizationModule, error) {
	modules, err := s.modules(ctx)
	if err != nil {
		return nil, err
	}
	for i := range modules {
		if modules[i].Spec.Resource == resourceType {
			return &modules[i], nil
		}
	}
	return nil, errResourceTypeNotFound
}

func (s *Server) upstreamURL(storeID string, requestURL *url.URL) (*url.URL, error) {
	if s.APIURL == "" {
		return nil, errors.New("OPENFGA_API_URL is required")
	}
	base, err := url.Parse(s.APIURL)
	if err != nil {
		return nil, err
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("OPENFGA_API_URL %q must include scheme and host", s.APIURL)
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/stores/" + url.PathEscape(storeID) + requestURL.EscapedPath()
	base.RawQuery = requestURL.RawQuery
	return base, nil
}

func (s *Server) httpClient() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	return http.DefaultClient
}

func (s *Server) tupleReader() TupleReader {
	if s.TupleReader != nil {
		return s.TupleReader
	}
	return &OpenFGATupleReader{APIURL: s.APIURL, APIToken: s.APIToken}
}

func (s *Server) namespace() string {
	if s.Namespace != "" {
		return s.Namespace
	}
	return DefaultNamespace
}

func (s *Server) releaseName() string {
	if s.ReleaseName != "" {
		return s.ReleaseName
	}
	return DefaultReleaseName
}

type accessSchemaResponse struct {
	ResourceType string                                    `json:"resourceType"`
	Topology     map[string]authv1.TopologyRelation        `json:"topology,omitempty"`
	Roles        map[string]authv1.AuthorizationRole       `json:"roles"`
	Permissions  map[string]authv1.AuthorizationPermission `json:"permissions,omitempty"`
}

type roleAssignmentsResponse struct {
	ResourceType string              `json:"resourceType"`
	ResourceID   string              `json:"resourceId"`
	Assignments  map[string][]string `json:"assignments"`
}

func authZENProxyPaths() []string {
	return []string{
		evaluationPath,
		evaluationsPath,
		resourceSearchPath,
		subjectSearchPath,
		actionSearchPath,
	}
}

func copyHeader(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "method is not allowed")
}

func statusForError(err error) int {
	switch {
	case errors.Is(err, errStableReleaseUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, errResourceTypeNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}
