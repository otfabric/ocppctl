// SPDX-License-Identifier: MIT

package ocpperr

import "encoding/json"

func jsonRaw(b []byte) json.RawMessage { return json.RawMessage(b) }
