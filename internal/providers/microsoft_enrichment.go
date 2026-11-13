package providers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
)

func (p *Microsoft) enrich(ctx context.Context, accessToken string, identity Identity) (Identity, []string) {
	unavailable := make([]string, 0, 2)
	profileAccepted := false
	if ctx.Err() == nil {
		profile, err := observe(ctx, "profile_retrieval", func(ctx context.Context) (microsoftProfile, error) {
			return p.profile(ctx, accessToken)
		})
		if err == nil {
			profileAccepted = true
			identity.Origin["profile"] = profile.origin
			if email := nilIfEmpty(profile.Mail); email != nil {
				identity.Email = email
			}
			if name := nilIfEmpty(profile.DisplayName); name != nil {
				identity.Name = name
			}
		}
	}
	if !profileAccepted {
		unavailable = append(unavailable, "profile")
	}
	photoAccepted := false
	if ctx.Err() == nil {
		photo, err := observe(ctx, "photo_retrieval", func(ctx context.Context) (microsoftPhoto, error) {
			return p.photo(ctx, accessToken)
		})
		if err == nil {
			photoAccepted = true
			identity.Origin["photo"] = photo.origin
			identity.AvatarURL = photo.avatarURL
		}
	}
	if !photoAccepted {
		unavailable = append(unavailable, "photo")
	}
	return identity, unavailable
}

type microsoftProfile struct {
	Mail        *string `json:"mail"`
	DisplayName *string `json:"displayName"`
	origin      json.RawMessage
}

func (p *Microsoft) profile(ctx context.Context, accessToken string) (microsoftProfile, error) {
	raw, _, _, err := fetch(ctx, p.client, p.profileURL, accessToken)
	if err != nil {
		return microsoftProfile{}, err
	}
	if _, err := jsonObject(raw); err != nil {
		return microsoftProfile{}, err
	}
	var profile microsoftProfile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return microsoftProfile{}, err
	}
	profile.origin = raw
	return profile, nil
}

type microsoftPhoto struct {
	avatarURL *string
	origin    json.RawMessage
}

func (p *Microsoft) photo(ctx context.Context, accessToken string) (microsoftPhoto, error) {
	photo, contentType, status, err := fetchWithAccept(ctx, p.client, p.photoURL, accessToken, "image/*")
	encodedPhoto := json.RawMessage("null")
	var avatarURL *string
	if err != nil && status != http.StatusNotFound {
		return microsoftPhoto{}, failure("photo retrieval", err)
	}
	if err == nil {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || len(mediaType) < 6 || mediaType[:6] != "image/" || len(photo) == 0 {
			return microsoftPhoto{}, failure("photo retrieval", errors.New("provider photo has invalid content"))
		}
		base64Photo := base64.StdEncoding.EncodeToString(photo)
		dataURL := "data:" + mediaType + ";base64," + base64Photo
		avatarURL = &dataURL
		encodedPhoto, err = json.Marshal(struct {
			ContentType string `json:"content_type"`
			DataBase64  string `json:"data_base64"`
		}{ContentType: contentType, DataBase64: base64Photo})
		if err != nil {
			return microsoftPhoto{}, failure("photo encoding", err)
		}
	}
	return microsoftPhoto{avatarURL: avatarURL, origin: encodedPhoto}, nil
}
