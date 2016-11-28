package gengokit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/TuneLab/go-truss/svcdef"
)

var gopath []string

func init() {
	gopath = filepath.SplitList(os.Getenv("GOPATH"))
}

func TestNewExecutor(t *testing.T) {
	const def = `
		syntax = "proto3";

		// General package
		package general;

		import "google.golang.org/genproto/googleapis/api/serviceconfig/annotations.proto";

		// RequestMessage is so foo
		message RequestMessage {
			string input = 1;
		}

		// ResponseMessage is so bar
		message ResponseMessage {
			string output = 1;
		}

		// ProtoService is a service
		service ProtoService {
			// ProtoMethod is simple. Like a gopher.
			rpc ProtoMethod (RequestMessage) returns (ResponseMessage) {
				// No {} in path and no body, everything is in the query
				option (google.api.http) = {
					get: "/route"
				};
			}
		}
	`
	sd, err := svcdef.NewFromString(def, gopath)
	if err != nil {
		t.Fatal(err)
	}

	conf := Config{
		GoPackage: "github.com/TuneLab/go-truss/gengokit/general-service",
		PBPackage: "github.com/TuneLab/go-truss/gengokit/general-service",
	}

	te, err := NewExecutor(sd, conf)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := te.PackageName, sd.PkgName; got != want {
		t.Fatalf("\n`%v` was PackageName\n`%v` was wanted", got, want)
	}
}
