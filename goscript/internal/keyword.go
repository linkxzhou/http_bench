package internal

import (
	"fmt"
	"strings"
)

var kindName = map[BasicKind]string{
	Var:             "Variable",
	Const:           "Constant",
	TypeName:        "Struct",
	Function:        "Function",
	BuiltinFunction: "Function",
}

type KeywordInfo struct {
	Label           string `json:"label"`
	Kind            string `json:"kind"`
	InsertText      string `json:"insertText"`
	InsertTextRules string `json:"insertTextRules"`
}

func Keywords() []*KeywordInfo {
	keywords := make([]*KeywordInfo, 0)
	for _, pkg := range GetAllPackages() {
		for _, object := range pkg.Objects {
			info := KeywordInfo{
				Label:           fmt.Sprintf("%s.%s", pkg.Name, object.Name),
				Kind:            kindName[object.Kind],
				InsertText:      "",
				InsertTextRules: "",
			}

			if info.Kind == "Function" {
				inParam := make([]string, 0)
				for i := 0; i < object.Type.NumIn(); i++ {
					inParam = append(inParam, fmt.Sprintf("${%d:%s}", i+1, object.Type.In(i).String()))
				}
				info.InsertText = fmt.Sprintf("%s(%s)", info.Label, strings.Join(inParam, ","))
				info.InsertTextRules = "InsertAsSnippet"
			} else {
				info.InsertText = pkg.Name + "." + object.Name
			}
			keywords = append(keywords, &info)
		}
	}

	return keywords
}
