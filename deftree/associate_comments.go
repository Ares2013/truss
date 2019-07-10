package deftree

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	log "github.com/Sirupsen/logrus"
	plugin "github.com/gogo/protobuf/protoc-gen-gogo/plugin"
)

func init() {
	// Output to stderr instead of stdout, could also be a file.
	log.SetOutput(os.Stderr)
	// Force colors in logs
	log.SetFormatter(&log.TextFormatter{
		ForceColors: true,
	})

	// Only log the warning severity or above.
	log.SetLevel(log.InfoLevel)
}

// cleanStr make strings nicely printable
func cleanStr(s string) string {
	cleanval := strings.Replace(s, "\n", "\\n", -1)
	cleanval = strings.Replace(cleanval, "\t", "\\t", -1)
	cleanval = strings.Replace(cleanval, "\"", "\\\"", -1)
	return cleanval
}

// Parses a protobuf string to return the 'label' of the field, if it exists.
func protoFieldLabel(proto_tag string) string {
	comma_split := strings.Split(proto_tag, ",")
	if len(comma_split) > 3 {
		eq_split := strings.Split(comma_split[3], "=")
		if len(eq_split) > 1 {
			return eq_split[1]
		}
	}
	return ""
}

// Given a value representing a protobuf message, return the go-field (wrapped
// in `reflect.Value`) which has the protobuf "field number" corresponding to
// the `proto_field` parameter.
func getProtobufField(proto_field int, proto_msg reflect.Value) (reflect.Value, string, error) {

	// Iterate through the fields of the struct, finding the field with the
	// struct tag indicating that that field correlates to the protobuf field
	// we're looking for.
	for n := 0; n < proto_msg.Type().NumField(); n++ {
		var typeField reflect.StructField = proto_msg.Type().Field(n)

		// Get the protobuf field number from the tag and check if it matches
		// the one we're looking for.
		pfield_n := -1
		tag := typeField.Tag.Get("protobuf")
		field_label := protoFieldLabel(tag)
		if len(tag) != 0 {
			pfield_n, _ = strconv.Atoi(strings.Split(tag, ",")[1])
		}

		if pfield_n != -1 && pfield_n == proto_field {
			// Found the correct field, return it and its label
			return proto_msg.Field(n), field_label, nil
		}
	}
	// Couldn't find a proto field with the given index
	return proto_msg, "", fmt.Errorf("Couldn't find a proto field with the given index '%v'", proto_field)
}

// Given a `reflect.Value` struct representing an array-indexible collection,
// return the `index`th item of that collection, wrapped in `reflect.Value`.
func getCollectionIndex(node reflect.Value, index int) reflect.Value {
	if index >= node.Len() {
		panic(fmt.Sprintf("The node '%v' is of length '%v', cannot access index '%v'", node, node.Len(), index))
	}
	return node.Index(index)
}

// Converts a SourceLocation path into a "NamePath", an array of names of
// objects, each nested within the last. Does this by walking backward through
// the integer path in increments of two integers. The integer path almost
// always follows a pattern of the first referring to a number in a field, and
// the second referring to an index-th entry in the array that that field
// represents. buildNamePath walks the integer path, finding the names of these
// entries and adding those names to the end of "NamePath". The returned slice
// of strings thus represents the names of objects and their implicit parents,
// which is used to find the location in the Deftree that matches the integer
// path.
//
// For example, there may be SourceLocation with a comment "spam eggs" and a
// path like [ 4 2 6 0 ]. To find the actual unit of code referred to by this
// SourceLocation we must walk its path. Walking the path goes like so:
//
//     From the root file, go to the 4th field
//       The 4th field of the file is the list of messages
//       Within the list of messages, go to the 2nd entry
//         The 2nd entry is a message named Foo
//         Within Foo, go to the 6th field
//           The 6th field of Foo is the list of fields within Foo
//           Within the list of fields, go to the 0th entry
//             The 0th entry is a field named 'baz'
//
// Note that there is a notion that fields have "numbers", as though they're
// ordered somehow. In the language Go, which is what we're using, fields of a
// struct don't have any "order" to them, so the idea of accessing the "ith"
// field makes no sense from a Go-native perspective. However, Protobuf does
// have this notion of fields having order, so a path made entirely of integers
// makes sense. So how is the data within Protobuf field numbers integrated
// into Go?
//
// The way Go is able to have numbered fields is by adding those numbers to the
// tags on the fields of the Go structs generated by the `protoc` compiler.
//
// Back onto the subject of the "NamePath", during the walking process we found
// that there where various concrete entries with names that composed the path.
// Those names, in the order we encountered them where:
//
//     "Foo"
//     "baz"
//
// If stored in the order we found them, we could imply that "the root file
// object contains some immediate child with the name 'Foo', and 'Foo' has some
// immediate child named 'baz'."
//
// This idea that there are just simple objects or nodes that contain child
// objects with some name is exactly how a Deftree is constructed and
// navigated. Every node within the Deftree implements the "Describable"
// interface, guaranteeing that it has a Name, a Description (comments about
// the node), and a GetByName method which allows you to query that node for
// any child nodes with the name you specify.
func buildNamePath(path []int32, node reflect.Value) ([]string, error) {
	log.WithFields(log.Fields{
		"path": path,
		"node": node.Type().String(),
	}).Debug("buildNamePath called with")
	var st_name string
	switch node.Kind() {
	case reflect.String:
		st_name = node.Interface().(string)
	case reflect.Ptr:
		node = node.Elem()
	default:
		if node.Kind() != reflect.Struct {
			err := fmt.Errorf("walkNextStruct expected struct, found '%v'", node.Kind())
			return nil, err
		} else {
			st_name = *node.FieldByName("Name").Interface().(*string)
		}
	}

	// Derive special information about this location, since it is the terminus
	// of the path
	if len(path) == 0 {
		return []string{st_name}, nil
	}

	field, _, err := getProtobufField(int(path[0]), node)
	if err != nil {
		return nil, err
	}

	// If the path ends here, then the path is indicating this field, and not
	// anything more specific
	if len(path) == 1 {
		err := fmt.Errorf("Comment somehow attached to a field label, time to panic!\n%v\n%v", path, node)
		return nil, err
	}

	// Since everything after this point is assuming that field is a slice, if
	// it's not we recurse
	if field.Kind() != reflect.Slice {
		log.WithFields(log.Fields{
			"field_type": field.Type().String(),
		}).Debug("The given field is not a slice, recursing")
		rv, err := buildNamePath(path[1:], field)
		if err != nil {
			return nil, err
		}
		return append([]string{st_name}, rv...), nil
	}

	if int(path[1]) >= field.Len() {
		err := fmt.Errorf("Second item in path ('%v') is longer than length of current field ('%v').", path[1], field.Len())
		return nil, err
	}
	next_node := getCollectionIndex(field, int(path[1]))

	// Dereference the returned field, if it exists
	var clean_next reflect.Value
	if next_node.Kind() == reflect.Ptr {
		clean_next = next_node.Elem()
	} else {
		clean_next = next_node
	}

	log.WithFields(log.Fields{
		"field_type": field.Type().String(),
	}).Debug("The given field is a slice")
	rv, err := buildNamePath(path[2:], clean_next)
	if err != nil {
		return nil, err
	}
	return append([]string{st_name}, rv...), nil
}

// Takes a comment and scrubs it of any extraneous artifacts (newlines, extra
// slashes, extra asterisks, etc)
func scrubComments(comment string) string {
	comment = strings.Replace(comment, "\n/ ", "\n", -1)
	comment = strings.Replace(comment, "\n/", "\n", -1)

	beginning_slash := regexp.MustCompile("^/*\\s*")
	trailing_whitespace := regexp.MustCompile("\\s*$")
	line_trail_ws := regexp.MustCompile("\\s+\n")

	comment = beginning_slash.ReplaceAllString(comment, "")
	comment = trailing_whitespace.ReplaceAllString(comment, "")
	comment = line_trail_ws.ReplaceAllString(comment, "\n")

	return comment
}

// AssociateComments walks the provided CodeGeneratorRequest finding comments
// and then copying them into their corresponding location within the deftree.
func AssociateComments(dt Deftree, req *plugin.CodeGeneratorRequest) {
	for _, file := range req.GetProtoFile() {
		// Skip comments for files outside the main one being considered
		skip := true
		for _, gen := range req.FileToGenerate {
			if file.GetName() == gen {
				skip = false
			}
		}
		if skip {
			continue
		}
		info := file.GetSourceCodeInfo()
		for _, location := range info.GetLocation() {
			lead := location.GetLeadingComments()
			// Only walk the tree if this source code location has a comment
			// located with it. Not all source locations have valid paths, but
			// all sourcelocations with comments must point to concrete things,
			// so only recurse on those.
			if len(lead) > 1 || len(location.LeadingDetachedComments) > 1 {
				lf := log.Fields{
					"location":                 location.GetPath(),
					"leading comment":          cleanStr(lead),
					"leading detached comment": location.LeadingDetachedComments,
				}

				// Only known case where a comment is attached not to an
				// instance of a struct, but to a field directly. And it's when
				// you comment on the package declaration itself.
				if len(location.Path) == 1 {
					log.WithFields(lf).Debug("Comment describes package name")
					dt.SetDescription(scrubComments(lead))
				} else {
					log.WithFields(lf).Debug("Comment is attached to some location, finding")
					name_path, err := buildNamePath(location.Path, reflect.ValueOf(*file))
					if err != nil {
						log.Debugf("Couldn't place comment '%v' due to error traversing tree: %v", cleanStr(lead), err)
					} else {
						err = dt.SetComment(name_path, scrubComments(lead))
						if err != nil && !strings.Contains(err.Error(), "cannot find node") {
							log.Errorf("cannot set comment: %v", err)
						}
					}
				}
			}
		}
	}
}
