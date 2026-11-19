package auth

import (
	"errors"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

type Issuer struct {
	secret []byte
	ttl    time.Duration
}

func NewIssuer(secret []byte, ttl time.Duration) *Issuer {
	return &Issuer{secret: secret, ttl: ttl}
}

func (i *Issuer) Issue(userID int64, role string) (string, time.Time, error) {
	exp := time.Now().Add(i.ttl)
	c := Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(userID, 10),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(exp),
			Issuer:    "typsteditor",
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(i.secret)
	return tok, exp, err
}

func (i *Issuer) Parse(token string) (int64, error) {
	c := &Claims{}
	_, err := jwt.ParseWithClaims(token, c, func(t *jwt.Token) (any, error) {
		return i.secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("typsteditor"), jwt.WithExpirationRequired())
	if err != nil {
		return 0, err
	}
	id, err := strconv.ParseInt(c.Subject, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("sujeto inválido")
	}
	return id, nil
}
