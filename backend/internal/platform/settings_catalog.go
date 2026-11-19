package platform

type settingOption struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	Category    string `json:"category"`
	Default     any    `json:"default"`
}

func settingCatalog() []settingOption {
	options := []settingOption{
		{Key: "brand.platform_name", Label: "平台名称", Description: "显示在登录页、导航栏及浏览器标签页。", Kind: "text", Category: "主题与品牌"},
		{Key: "brand.platform_tagline", Label: "平台副标题", Description: "显示在侧边栏的平台说明，留空可隐藏。", Kind: "text", Category: "主题与品牌"},
		{Key: "brand.logo_url", Label: "平台 Logo", Description: "图片 URL 或站内路径；留空恢复默认标识。上传图片请使用主题设置。", Kind: "text", Category: "主题与品牌"},
		{Key: "theme.nav_bg", Label: "导航背景颜色", Description: "同步应用于左侧菜单和顶部导航。", Kind: "color", Category: "主题与品牌"},
		{Key: "theme.nav_text", Label: "导航文字颜色", Description: "导航区域的文字与图标颜色。", Kind: "color", Category: "主题与品牌"},
		{Key: "app.footer_text", Label: "侧边栏底部文案", Description: "侧边栏底部状态文字。", Kind: "text", Category: "应用显示"},
		{Key: "app.footer_visible", Label: "显示侧边栏底部状态", Description: "关闭后隐藏侧边栏底部状态区。", Kind: "boolean", Category: "应用显示"},
		{Key: "app.login_notice", Label: "登录页公告", Description: "向登录用户显示公告，留空不显示。", Kind: "textarea", Category: "应用显示"},
		{Key: "smtp.host", Label: "邮件服务器", Description: "SMTP 主机名或 IP，不含协议和端口。", Kind: "text", Category: "邮件通知"},
		{Key: "smtp.port", Label: "邮件服务端口", Description: "范围 1–65535；通常使用 587（STARTTLS）或 465（直接 TLS）。", Kind: "number", Category: "邮件通知"},
		{Key: "smtp.username", Label: "邮件登录账号", Description: "SMTP 认证账号；无认证的邮件中继可留空。", Kind: "text", Category: "邮件通知"},
		{Key: "smtp.password", Label: "邮件登录密码", Description: "仅填写新密码时更新；已有密码不会显示。", Kind: "secret", Category: "邮件通知"},
		{Key: "smtp.from", Label: "发件地址", Description: "填写单个发件邮箱，例如 noreply@example.com。", Kind: "text", Category: "邮件通知"},
		{Key: "smtp.tls", Label: "启用邮件传输加密", Description: "587 等端口启用 STARTTLS；465 端口始终使用直接 TLS。认证发信必须加密。", Kind: "boolean", Category: "邮件通知"},
	}
	defaults := allSettingDefaults()
	for i := range options {
		options[i].Default = defaults[options[i].Key]
	}
	return options
}
