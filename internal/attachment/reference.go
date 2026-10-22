package attachment

import (
	"github.com/cloudwego/eino/schema"
	"github.com/ravenmk2/wispjar/internal/session"
)

const ReferencePrefix = "wispjar-attachment:"

func ImageReference(ref session.AttachmentRef) schema.MessagePartCommon {
	url := ReferencePrefix + ref.ID
	return schema.MessagePartCommon{URL: &url, MIMEType: ref.MIME}
}
