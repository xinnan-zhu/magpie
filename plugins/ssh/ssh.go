// Package ssh contributes the optional desktop page for remote hosts.
package ssh

import (
	"github.com/yetone/magpie/internal/pluginrt"
	"github.com/yetone/magpie/internal/sshremote"
)

func init() {
	pluginrt.Register(pluginrt.Manifest{
		ID: "ssh", API: "^1", Requires: []string{"window"}, Name: "SSH remote hosts", Sub: "Manage another computer's Magpie through system SSH",
		Parts: []pluginrt.Part{{Slot: "nav.page", Key: "ssh"}}, Client: []string{"i18n.js", "ssh.css", "client.js"},
		Apply: func(c *pluginrt.Ctx) error {
			sshremote.Desktop.Enable()
			c.OnDispose(sshremote.Desktop.Close)
			return nil
		},
	})
}
