package handler

import (
	"strings"

	"baremetal-manager/backend/internal/repository"

	"github.com/gin-gonic/gin"
)

// 品牌与主题默认值，未配置时使用。
const (
	defaultPlatformName    = "METALOPS"
	defaultPlatformTagline = "BAREMETAL CONTROL PLANE"
	defaultNavBackground   = "#101e2b"
	defaultNavText         = "#a9bbc7"
	defaultFooterText      = "控制平面在线"
	defaultLoginNotice     = ""
)

// RegisterBrandRoutes 注册公开的品牌信息路由，登录页无需认证即可读取。
// 路由前缀: /api/v1/brand
func RegisterBrandRoutes(rg *gin.RouterGroup, store *repository.Store) {
	rg.GET("/brand", getBrand(store))
}

// getBrand 返回平台名称、副标题与 Logo，供登录页和侧边栏展示。
func getBrand(store *repository.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := defaultPlatformName
		tagline := defaultPlatformTagline
		logo := ""
		navBackground := defaultNavBackground
		navText := defaultNavText
		footerText := defaultFooterText
		footerVisible := true
		loginNotice := defaultLoginNotice

		if setting, err := store.GetSystemSettingByKey("brand.platform_name"); err == nil {
			if value := strings.TrimSpace(setting.Value); value != "" {
				name = value
			}
		}
		if setting, err := store.GetSystemSettingByKey("brand.platform_tagline"); err == nil {
			if value := strings.TrimSpace(setting.Value); value != "" {
				tagline = value
			}
		}
		if setting, err := store.GetSystemSettingByKey("brand.logo_url"); err == nil {
			logo = strings.TrimSpace(setting.Value)
		}
		if setting, err := store.GetSystemSettingByKey("theme.nav_bg"); err == nil {
			if value := strings.TrimSpace(setting.Value); value != "" {
				navBackground = value
			}
		}
		if setting, err := store.GetSystemSettingByKey("theme.nav_text"); err == nil {
			if value := strings.TrimSpace(setting.Value); value != "" {
				navText = value
			}
		}
		if setting, err := store.GetSystemSettingByKey("app.footer_text"); err == nil {
			if value := strings.TrimSpace(setting.Value); value != "" {
				footerText = value
			}
		}
		if setting, err := store.GetSystemSettingByKey("app.footer_visible"); err == nil {
			footerVisible = strings.TrimSpace(setting.Value) != "false"
		}
		if setting, err := store.GetSystemSettingByKey("app.login_notice"); err == nil {
			loginNotice = strings.TrimSpace(setting.Value)
		}

		OK(c, gin.H{
			"platform_name":    name,
			"platform_tagline": tagline,
			"logo_url":         logo,
			"nav_bg_color":     navBackground,
			"nav_text_color":   navText,
			"footer_text":      footerText,
			"footer_visible":   footerVisible,
			"login_notice":     loginNotice,
		})
	}
}
