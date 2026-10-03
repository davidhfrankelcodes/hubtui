package hub

import "net/url"

// WebURL is the repository's page on hub.docker.com, or its tags list
// narrowed to tag when tag is not empty. Official images live under /_/,
// everything else under /r/.
func WebURL(repo Repo, tag string) string {
	u := "https://hub.docker.com/r/" + repo.Namespace + "/" + repo.Name
	if repo.Official() {
		u = "https://hub.docker.com/_/" + repo.Name
	}
	if tag == "" {
		return u
	}
	return u + "/tags?name=" + url.QueryEscape(tag)
}
