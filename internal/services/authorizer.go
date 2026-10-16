package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type AuthZService struct {
	key string
}

func NewAuthZService(key string) AuthZService {
	return AuthZService{key: key}
}

// issues an authz-token signed with this services key
func (s AuthZService) MintToken(username string, permissions []string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username":    username,
		"permissions": strings.Join(permissions, ","),
		"exp":         time.Now().Add(time.Hour).Unix(),
	})
	return token.SignedString([]byte(s.key))
}

func (s AuthZService) Authorize(ctx context.Context, permName string, objKind string, objId string) error {
	tokenString, ok := ctx.Value("authz-token").(string)
	if !ok {
		return errors.New("no token provided")
	}
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		return []byte(s.key), nil
	})
	if err != nil {
		return err
	}

	var permissions []string
	if claims, ok := token.Claims.(jwt.MapClaims); ok {
		if permissionsClaim, ok := claims["permissions"].(string); ok {
			permissions = strings.Split(permissionsClaim, ",")
		} else {
			return errors.New("custom claim permissions is not a string or does not exist")
		}
	} else {
		return errors.New("invalid claims type")
	}

	reqPerm := fmt.Sprintf("%s|%s|%s", permName, objKind, objId)
	for _, perm := range permissions {
		if perm == reqPerm {
			return nil
		}
	}

	return errors.New("required permission not found")
}
