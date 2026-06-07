package app

type Config struct {
	Port            int    `env:"PORT" envDefault:"8080"`
	DBPath          string `env:"DB_PATH" envDefault:"/data/memory.db"`
	DeepseekAPIKey  string `env:"DEEPSEEK_API_KEY"`
	DeepseekBaseURL string `env:"DEEPSEEK_BASE_URL" envDefault:"https://api.deepseek.com"`
	OpenAIAPIKey    string `env:"OPENAI_API_KEY"`
	OpenAIBaseURL   string `env:"OPENAI_BASE_URL" envDefault:"https://api.openai.com"`
	MemoryAuthToken string `env:"MEMORY_AUTH_TOKEN"`
}
