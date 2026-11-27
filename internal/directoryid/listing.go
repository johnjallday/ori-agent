package directoryid

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

var listingKey = []byte(rand.Text())

// ListingStamp lets two inert reads agree about the very same folder seen
// under the very same link. Never expose a device/inode or accept a stamp as
// an access token. Restarting the host deliberately invalidates all stamps.
func ListingStamp(owner, link, relative, rootIdentity, folderIdentity string) string {
	if owner == "" || link == "" || relative == "" || rootIdentity == "" || folderIdentity == "" {
		return ""
	}
	mac := hmac.New(sha256.New, listingKey)
	mac.Write([]byte(strings.Join([]string{owner, link, relative, rootIdentity, folderIdentity}, "\x00")))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
