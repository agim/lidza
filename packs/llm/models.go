package llm

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"
)

// Model is one model a provider offers to the configured key.
type Model struct {
	ID string `json:"id"`
	// Name is the provider's display name, when it gives one.
	Name string `json:"name,omitempty"`
	// Chat and Embed say what the model is for, as far as the provider
	// tells: both are true when it does not.
	Chat  bool `json:"chat"`
	Embed bool `json:"embed"`
}

// ErrNoModelList is what ListModels returns for a provider that has no
// list of models (fake).
var ErrNoModelList = errors.New("llm: this provider has no model list")

// modelLister is a provider that can list its models.
type modelLister interface {
	Models(ctx context.Context) ([]Model, error)
}

// ListModels asks the provider cfg names which models its key can use:
// what the admin pages suggest and check a model name against before it
// is saved. A refused key is an *Error with Status 401 or 403.
func ListModels(ctx context.Context, cfg Config) ([]Model, error) {
	return listModels(ctx, cfg, chatSettings)
}

// ListEmbedModels is ListModels for the embeddings provider: errors name
// the EMBED_ settings.
func ListEmbedModels(ctx context.Context, cfg Config) ([]Model, error) {
	return listModels(ctx, cfg, embedSettings)
}

func listModels(ctx context.Context, cfg Config, names settingNames) ([]Model, error) {
	p, err := newProvider(cfg, names)
	if err != nil {
		return nil, err
	}
	l, ok := p.(modelLister)
	if !ok {
		return nil, ErrNoModelList
	}
	models, err := l.Models(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

// HasModel reports whether id is among models. An Ollama tag without a
// version ("llama3.2") names its ":latest".
func HasModel(models []Model, id string) bool {
	for _, m := range models {
		if m.ID == id || m.ID == id+":latest" {
			return true
		}
	}
	return false
}

// Closest returns up to n model ids nearest to id (shared prefix first,
// then edit distance), for saying what was probably meant.
func Closest(models []Model, id string, n int) []string {
	type scored struct {
		id    string
		score int
	}
	var all []scored
	for _, m := range models {
		prefix := 0
		for prefix < len(m.ID) && prefix < len(id) && m.ID[prefix] == id[prefix] {
			prefix++
		}
		all = append(all, scored{m.ID, distance(m.ID, id) - 2*prefix})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].score < all[j].score })
	var out []string
	for i := 0; i < len(all) && i < n; i++ {
		out = append(out, all[i].id)
	}
	return out
}

// distance is the Levenshtein distance between a and b.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// Models lists GET /v1/models, a page at a time.
func (p *anthropic) Models(ctx context.Context) ([]Model, error) {
	var out []Model
	after := ""
	for page := 0; page < 20; page++ {
		u := p.base + "/v1/models?limit=1000"
		if after != "" {
			u += "&after_id=" + url.QueryEscape(after)
		}
		var r struct {
			Data []struct {
				ID          string `json:"id"`
				DisplayName string `json:"display_name"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err := getJSON(ctx, "anthropic", u, p.headers(), &r); err != nil {
			return nil, err
		}
		for _, m := range r.Data {
			out = append(out, Model{ID: m.ID, Name: m.DisplayName, Chat: true})
		}
		if !r.HasMore || r.LastID == "" {
			break
		}
		after = r.LastID
	}
	return out, nil
}

// Models lists GET /v1/models: every model of the account (or the
// compatible server's loaded ones). OpenAI's list mixes in speech,
// image and moderation models; those are neither chat nor embedding.
func (p *openai) Models(ctx context.Context) ([]Model, error) {
	var r struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := getJSON(ctx, p.Name(), p.base+"/v1/models", p.headers(), &r); err != nil {
		return nil, err
	}
	var out []Model
	for _, m := range r.Data {
		model := Model{ID: m.ID, Chat: true, Embed: true}
		if p.Name() == "openai" {
			model.Embed = strings.Contains(m.ID, "embedding")
			model.Chat = !model.Embed && !openaiOther(m.ID)
		}
		out = append(out, model)
	}
	return out, nil
}

// openaiOther reports an OpenAI model that neither chats nor embeds.
func openaiOther(id string) bool {
	for _, s := range []string{"tts", "whisper", "dall-e", "moderation", "image", "transcribe", "audio", "realtime", "sora", "davinci", "babbage"} {
		if strings.Contains(id, s) {
			return true
		}
	}
	return false
}

// Models lists GET /v1beta/models, a page at a time, with what each
// model supports.
func (p *google) Models(ctx context.Context) ([]Model, error) {
	var out []Model
	token := ""
	for page := 0; page < 20; page++ {
		u := p.base + "/v1beta/models?pageSize=1000"
		if token != "" {
			u += "&pageToken=" + url.QueryEscape(token)
		}
		var r struct {
			Models []struct {
				Name        string   `json:"name"`
				DisplayName string   `json:"displayName"`
				Methods     []string `json:"supportedGenerationMethods"`
			} `json:"models"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := getJSON(ctx, "google", u, p.headers(), &r); err != nil {
			return nil, err
		}
		for _, m := range r.Models {
			model := Model{ID: strings.TrimPrefix(m.Name, "models/"), Name: m.DisplayName}
			for _, method := range m.Methods {
				switch method {
				case "generateContent":
					model.Chat = true
				case "embedContent", "batchEmbedContents":
					model.Embed = true
				}
			}
			out = append(out, model)
		}
		if r.NextPageToken == "" {
			break
		}
		token = r.NextPageToken
	}
	return out, nil
}

// Models lists the models pulled into the Ollama server (GET /api/tags).
func (p *ollama) Models(ctx context.Context) ([]Model, error) {
	var r struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := getJSON(ctx, "ollama", p.base+"/api/tags", nil, &r); err != nil {
		return nil, err
	}
	var out []Model
	for _, m := range r.Models {
		out = append(out, Model{ID: m.Name, Chat: true, Embed: true})
	}
	return out, nil
}
