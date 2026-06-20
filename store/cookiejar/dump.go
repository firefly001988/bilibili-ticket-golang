// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cookiejar

import (
	"sort"
)

func (j *Jar) AllPersistentEntries() []CookieEntries {
	j.mu.Lock()
	defer j.mu.Unlock()

	var entries []CookieEntries
	for _, submap := range j.entries {
		for _, e := range submap {
			if e.Persistent {
				entries = append(entries, e)
			}
		}
	}
	sort.Sort(byCanonicalHost{entries})
	return entries
}

func (j *Jar) AllEntries() []CookieEntries {
	j.mu.Lock()
	defer j.mu.Unlock()
	var entries []CookieEntries
	for _, submap := range j.entries {
		for _, entry := range submap {
			entries = append(entries, entry)
		}
	}
	sort.Sort(byCanonicalHost{entries})
	return entries
}
