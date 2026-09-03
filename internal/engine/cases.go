package engine

import "sort"

// ExpandTestCases turns a declarative case matrix into independent test
// executions. A case is intentionally a named mapping rather than an anonymous
// list so IDs stay stable when cases are reordered.
func ExpandTestCases(test TestSpec) []TestSpec {
	if len(test.Cases) == 0 {
		if test.BaseID == "" {
			test.BaseID = test.ID
		}
		return []TestSpec{test}
	}
	keys := make([]string, 0, len(test.Cases))
	for id := range test.Cases {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	out := make([]TestSpec, 0, len(keys))
	for _, caseID := range keys {
		c := test.Cases[caseID]
		x := test
		x.BaseID = test.ID
		x.CaseID = caseID
		x.ID = test.ID + "[" + caseID + "]"
		x.Variables = deepMerge(cloneMap(test.Variables), cloneMap(c.Variables))
		x.Skip = test.Skip || c.Skip
		if c.Name != "" {
			if x.Name != "" {
				x.Name += " [" + c.Name + "]"
			} else {
				x.Name = c.Name
			}
		} else if x.Name != "" {
			x.Name += " [" + caseID + "]"
		}
		x.Cases = nil
		out = append(out, x)
	}
	return out
}
