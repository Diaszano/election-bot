// Package migrations embeds the versioned durable schemas.
package migrations

import _ "embed"

//go:embed 001_initial.sql
var Initial string

//go:embed 002_channel_notifications.sql
var ChannelNotifications string
