//+build ignore

// msg_generate.go is meant to run with go generate. It will use
// go/{importer,types} to track down all the RR struct types. Then for each type
// it will generate pack/unpack methods based on the struct tags. The generated source is
// written to zmsg.go, and is meant to be checked into git.
package main

import (
	"bytes"
	"fmt"
	"go/format"
	"go/importer"
	"go/types"
	"log"
	"os"
)

// What types are we generating, should be kept in sync with typeToUnpack in msg.go
var generate = map[string]bool{
	"AAAA": true,
	"A":    true,
	"L32":  true,
	"MX":   true,
	"NID":  true,
}

func shouldGenerate(name string) bool {
	_, ok := generate[name]
	return ok
}

// For later: IPSECKEY is weird.

var packageHdr = `
// *** DO NOT MODIFY ***
// AUTOGENERATED BY go generate from msg_generate.go

package dns

//import (
	//"encoding/base64"
	//"net"
//)

`

// getTypeStruct will take a type and the package scope, and return the
// (innermost) struct if the type is considered a RR type (currently defined as
// those structs beginning with a RR_Header, could be redefined as implementing
// the RR interface). The bool return value indicates if embedded structs were
// resolved.
func getTypeStruct(t types.Type, scope *types.Scope) (*types.Struct, bool) {
	st, ok := t.Underlying().(*types.Struct)
	if !ok {
		return nil, false
	}
	if st.Field(0).Type() == scope.Lookup("RR_Header").Type() {
		return st, false
	}
	if st.Field(0).Anonymous() {
		st, _ := getTypeStruct(st.Field(0).Type(), scope)
		return st, true
	}
	return nil, false
}

func main() {
	// Import and type-check the package
	pkg, err := importer.Default().Import("github.com/miekg/dns")
	fatalIfErr(err)
	scope := pkg.Scope()

	// Collect actual types (*X)
	var namedTypes []string
	for _, name := range scope.Names() {
		o := scope.Lookup(name)
		if o == nil || !o.Exported() {
			continue
		}
		if st, _ := getTypeStruct(o.Type(), scope); st == nil {
			continue
		}
		if name == "PrivateRR" {
			continue
		}

		// Check if corresponding TypeX exists
		if scope.Lookup("Type"+o.Name()) == nil && o.Name() != "RFC3597" {
			log.Fatalf("Constant Type%s does not exist.", o.Name())
		}

		namedTypes = append(namedTypes, o.Name())
	}

	b := &bytes.Buffer{}
	b.WriteString(packageHdr)

	fmt.Fprint(b, "// pack*() functions\n\n")
	for _, name := range namedTypes {
		o := scope.Lookup(name)
		st, isEmbedded := getTypeStruct(o.Type(), scope)
		if isEmbedded || !shouldGenerate(name) {
			continue
		}

		fmt.Fprintf(b, "func (rr *%s) pack(msg []byte, off int, compression map[string]int, compress bool) (int, error) {\n", name)
		fmt.Fprint(b, `off, err := packHeader(rr.Hdr, msg, off, compression, compress)
if err != nil {
	return off, err
}
headerEnd := off
`)
		for i := 1; i < st.NumFields(); i++ {
			o := func(s string) {
				fmt.Fprintf(b, s, st.Field(i).Name())
				fmt.Fprint(b, `if err != nil {
return off, err
}
`)
			}

			//if _, ok := st.Field(i).Type().(*types.Slice); ok {
			//switch st.Tag(i) {
			//case `dns:"-"`:
			//// ignored
			//case `dns:"cdomain-name"`, `dns:"domain-name"`, `dns:"txt"`:
			//o("for _, x := range rr.%s { l += len(x) + 1 }\n")
			//default:
			//log.Fatalln(name, st.Field(i).Name(), st.Tag(i))
			//}
			//continue
			//}

			switch st.Tag(i) {
			case `dns:"-"`:
				// ignored
			case `dns:"cdomain-name"`:
				fallthrough
			case `dns:"domain-name"`:
				o("off, err = PackDomainName(rr.%s, msg, off, compression, compress)\n")
			case `dns:"a"`:
				o("off, err = packDataA(rr.%s, msg, off)\n")
			case `dns:"aaaa"`:
				o("off, err = packDataAAAA(rr.%s, msg, off)\n")
			case `dns:"uint48"`:
				o("off, err = packUint64(rr.%s, msg, off, len(msg), true)\n")
			case "":
				switch st.Field(i).Type().(*types.Basic).Kind() {
				case types.Uint8:
					o("off, err = packUint8(rr.%s, msg, off, len(msg))\n")
				case types.Uint16:
					o("off, err = packUint16(rr.%s, msg, off, len(msg))\n")
				case types.Uint32:
					o("off, err = packUint32(rr.%s, msg, off, len(msg))\n")
				case types.Uint64:
					o("off, err = packUint64(rr.%s, msg, off, len(msg), false)\n")
				case types.String:

				default:
					log.Fatalln(name, st.Field(i).Name())
				}
				//default:
				//log.Fatalln(name, st.Field(i).Name(), st.Tag(i))
			}
		}
		// We have packed everything, only now we know the rdlength of this RR
		fmt.Fprintln(b, "rr.Header().Rdlength = uint16(off- headerEnd)")
		fmt.Fprintln(b, "return off, nil }\n")
	}

	fmt.Fprint(b, "// unpack*() functions\n\n")
	for _, name := range namedTypes {
		o := scope.Lookup(name)
		st, isEmbedded := getTypeStruct(o.Type(), scope)
		if isEmbedded || !shouldGenerate(name) {
			continue
		}

		fmt.Fprintf(b, "func unpack%s(h RR_Header, msg []byte, off int) (RR, int, error) {\n", name)
		fmt.Fprint(b, `if dynamicUpdate(h) {
return nil, off, nil
	}
var err error
`)
		fmt.Fprintf(b, "rr := new(%s)\n", name)
		fmt.Fprintln(b, "rr.Hdr = h\n")
		for i := 1; i < st.NumFields(); i++ {
			o := func(s string) {
				fmt.Fprintf(b, s, st.Field(i).Name())
				fmt.Fprint(b, `if err != nil {
return rr, off, err
}
`)
			}

			//if _, ok := st.Field(i).Type().(*types.Slice); ok {
			//switch st.Tag(i) {
			//case `dns:"-"`:
			//// ignored
			//case `dns:"cdomain-name"`, `dns:"domain-name"`, `dns:"txt"`:
			//o("for _, x := range rr.%s { l += len(x) + 1 }\n")
			//default:
			//log.Fatalln(name, st.Field(i).Name(), st.Tag(i))
			//}
			//continue
			//}

			switch st.Tag(i) {
			case `dns:"-"`:
				// ignored
			case `dns:"cdomain-name"`:
				fallthrough
			case `dns:"domain-name"`:
				o("rr.%s, off, err = UnpackDomainName(msg, off)\n")
			case `dns:"a"`:
				o("rr.%s, off, err = unpackDataA(msg, off)\n")
			case `dns:"aaaa"`:
				o("rr.%s, off, err = unpackDataAAAA(msg, off)\n")
			case `dns:"uint48"`:
				o("rr.%s, off, err = unpackUint64(msg, off, true)\n")
			case "":
				switch st.Field(i).Type().(*types.Basic).Kind() {
				case types.Uint8:
					o("rr.%s, off, err = unpackUint8(msg, off)\n")
				case types.Uint16:
					o("rr.%s, off, err = unpackUint16(msg, off)\n")
				case types.Uint32:
					o("rr.%s, off, err = unpackUint32(msg, off)\n")
				case types.Uint64:
					o("rr.%s, off, err = unpackUint64(msg, off, false)\n")
				case types.String:

				default:
					log.Fatalln(name, st.Field(i).Name())
				}
				//default:
				//log.Fatalln(name, st.Field(i).Name(), st.Tag(i))
			}
			// If we've hit len(msg) we return without error.
			if i < st.NumFields()-1 {
				fmt.Fprintf(b, `if off == len(msg) {
return rr, off, nil
	}
`)
			}
		}
		fmt.Fprintf(b, "return rr, off, nil }\n\n")
	}

	// gofmt
	res, err := format.Source(b.Bytes())
	if err != nil {
		b.WriteTo(os.Stderr)
		log.Fatal(err)
	}

	// write result
	f, err := os.Create("zmsg.go")
	fatalIfErr(err)
	defer f.Close()
	f.Write(res)
}

func fatalIfErr(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
