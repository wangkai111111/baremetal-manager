package platform

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPXEOptionsRequirePlatformAdmin(t *testing.T) {
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user", user{ID: 1}) })
	router.GET("/pxe-services/options", (&App{}).pxeServices)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/pxe-services/options", nil))
	if w.Code != 403 {
		t.Fatalf("ordinary user read host inventory: %d", w.Code)
	}
}
