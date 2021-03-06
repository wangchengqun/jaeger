// Copyright (c) 2017 Uber Technologies, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package zipkin

import (
	"compress/gzip"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/apache/thrift/lib/go/thrift"
	"github.com/gorilla/mux"
	tchanThrift "github.com/uber/tchannel-go/thrift"

	"github.com/jaegertracing/jaeger/cmd/collector/app"
	"github.com/jaegertracing/jaeger/thrift-gen/zipkincore"
)

// APIHandler handles all HTTP calls to the collector
type APIHandler struct {
	zipkinSpansHandler app.ZipkinSpansHandler
}

// NewAPIHandler returns a new APIHandler
func NewAPIHandler(
	zipkinSpansHandler app.ZipkinSpansHandler,
) *APIHandler {
	return &APIHandler{
		zipkinSpansHandler: zipkinSpansHandler,
	}
}

// RegisterRoutes registers Zipkin routes
func (aH *APIHandler) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/api/v1/spans", aH.saveSpans).Methods(http.MethodPost)
}

func (aH *APIHandler) saveSpans(w http.ResponseWriter, r *http.Request) {
	bRead := r.Body
	defer r.Body.Close()

	if strings.Contains(r.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf(app.UnableToReadBodyErrFormat, err), http.StatusBadRequest)
			return
		}
		defer gz.Close()
		bRead = gz
	}

	bodyBytes, err := ioutil.ReadAll(bRead)
	if err != nil {
		http.Error(w, fmt.Sprintf(app.UnableToReadBodyErrFormat, err), http.StatusInternalServerError)
		return
	}

	contentType := r.Header.Get("Content-Type")
	var tSpans []*zipkincore.Span
	if contentType == "application/x-thrift" {
		tSpans, err = deserializeThrift(bodyBytes)
	} else if contentType == "application/json" {
		tSpans, err = DeserializeJSON(bodyBytes)
	} else {
		http.Error(w, "Unsupported Content-Type", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, fmt.Sprintf(app.UnableToReadBodyErrFormat, err), http.StatusBadRequest)
		return
	}

	if len(tSpans) > 0 {
		ctx, _ := tchanThrift.NewContext(time.Minute)
		if _, err = aH.zipkinSpansHandler.SubmitZipkinBatch(ctx, tSpans); err != nil {
			http.Error(w, fmt.Sprintf("Cannot submit Zipkin batch: %v", err), http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusAccepted)
}

func deserializeThrift(b []byte) ([]*zipkincore.Span, error) {
	buffer := thrift.NewTMemoryBuffer()
	buffer.Write(b)

	transport := thrift.NewTBinaryProtocolTransport(buffer)
	_, size, err := transport.ReadListBegin() // Ignore the returned element type
	if err != nil {
		return nil, err
	}

	// We don't depend on the size returned by ReadListBegin to preallocate the array because it
	// sometimes returns a nil error on bad input and provides an unreasonably large int for size
	var spans []*zipkincore.Span
	for i := 0; i < size; i++ {
		zs := &zipkincore.Span{}
		if err = zs.Read(transport); err != nil {
			return nil, err
		}
		spans = append(spans, zs)
	}

	return spans, nil
}
