package config

import (
	"time"

	"github.com/NikitaDmitryuk/telegram-media-server/internal/logutils"
	"github.com/NikitaDmitryuk/telegram-media-server/internal/utils"
)

const (
	DefaultAria2MaxPeers                = 200
	DefaultAria2MaxConnectionsPerServer = 16
	DefaultAria2Split                   = 16
	DefaultAria2BTMaxPeers              = 200
	DefaultAria2BTMaxOpenFiles          = 100
	DefaultAria2BTTrackerTimeout        = 60
	DefaultAria2BTTrackerInterval       = 60 // re-announce every 60s so peers are discovered if first announce fails or returns empty
	DefaultAria2Timeout                 = 60
	DefaultAria2MaxTries                = 5
	DefaultPasswordMinLength            = 8
	DefaultMaxConcurrentDownloads       = 3
	DefaultProgressUpdateInterval       = 3 * time.Second
	DefaultVideoMaxHeight               = 0             // Default: no max height limit (0 = disabled)
	DefaultYtdlpUpdateInterval          = 3 * time.Hour // Periodic yt-dlp update interval; 0 = disabled
	DefaultYtdlpUpdateMode              = "self"
	DefaultYtdlpPythonPath              = "python3"
	DefaultTMSAPIListen                 = "127.0.0.1:8080"
)

func NewConfig() (*Config, error) {
	config := &Config{
		BotToken:               getEnv("BOT_TOKEN", ""),
		MoviePath:              getEnv("MOVIE_PATH", ""),
		AdminPassword:          getEnv("ADMIN_PASSWORD", ""),
		RegularPassword:        getEnv("REGULAR_PASSWORD", ""),
		Lang:                   getEnv("LANG", "en"),
		TelegramProxy:          getEnv("TELEGRAM_PROXY", ""),
		Proxy:                  getEnv("CONTENT_PROXY", getEnv("PROXY", "")),
		ProxyDomains:           getEnv("CONTENT_PROXY_DOMAINS", getEnv("PROXY_DOMAINS", "")),
		LogLevel:               getEnv("LOG_LEVEL", "info"),
		LangPath:               getEnv("LANG_PATH", "/usr/local/share/telegram-media-server/locales"),
		ProwlarrURL:            getEnv("PROWLARR_URL", ""),
		ProwlarrAPIKey:         getEnv("PROWLARR_API_KEY", ""),
		TMSAPIEnabled:          getEnvBool("TMS_API_ENABLED", true),
		TMSAPIListen:           getEnv("TMS_API_LISTEN", DefaultTMSAPIListen),
		TMSAPIKey:              getEnv("TMS_API_KEY", ""),
		TMSWebhookURL:          getEnv("TMS_WEBHOOK_URL", ""),
		TMSWebhookToken:        getEnv("TMS_WEBHOOK_TOKEN", ""),
		TMSWebhookFormat:       getEnv("TMS_WEBHOOK_FORMAT", ""),
		YtdlpPath:              getEnv("YTDLP_PATH", "/usr/bin/yt-dlp"),
		YtdlpExtraArgs:         getEnv("YTDLP_EXTRA_ARGS", ""),
		YtdlpCookiesPath:       getEnv("YTDLP_COOKIES_PATH", ""),
		YtdlpUpdateMode:        getEnv("YTDLP_UPDATE_MODE", DefaultYtdlpUpdateMode),
		YtdlpPythonPath:        getEnv("YTDLP_PYTHON_PATH", DefaultYtdlpPythonPath),
		YtdlpUpdateOnStart:     getEnvBool("YTDLP_UPDATE_ON_START", true),
		YtdlpUpdateInterval:    getEnvDuration("YTDLP_UPDATE_INTERVAL", DefaultYtdlpUpdateInterval),
		QBittorrentURL:         getEnv("QBITTORRENT_URL", ""),
		QBittorrentUsername:    getEnv("QBITTORRENT_USERNAME", "admin"),
		QBittorrentPassword:    getEnv("QBITTORRENT_PASSWORD", "adminadmin"),
		TorrentFallbackToAria2: getEnvBool("TORRENT_FALLBACK_TO_ARIA2", false),

		DownloadSettings: DownloadConfig{
			MaxConcurrentDownloads: getEnvInt("MAX_CONCURRENT_DOWNLOADS", DefaultMaxConcurrentDownloads),
			DownloadTimeout:        getEnvDuration("DOWNLOAD_TIMEOUT", 0),
			ProgressUpdateInterval: getEnvDuration("PROGRESS_UPDATE_INTERVAL", DefaultProgressUpdateInterval),
		},

		SecuritySettings: SecurityConfig{
			PasswordMinLength: DefaultPasswordMinLength,
		},

		Aria2Settings: Aria2Config{
			MaxPeers:                 getEnvInt("ARIA2_MAX_PEERS", DefaultAria2MaxPeers),
			MaxConnectionsPerServer:  getEnvInt("ARIA2_MAX_CONNECTIONS_PER_SERVER", DefaultAria2MaxConnectionsPerServer),
			Split:                    getEnvInt("ARIA2_SPLIT", DefaultAria2Split),
			MinSplitSize:             getEnv("ARIA2_MIN_SPLIT_SIZE", "1M"),
			BTMaxPeers:               getEnvInt("ARIA2_BT_MAX_PEERS", DefaultAria2BTMaxPeers),
			BTRequestPeerSpeedLimit:  getEnv("ARIA2_BT_REQUEST_PEER_SPEED_LIMIT", "0"),
			BTMaxOpenFiles:           getEnvInt("ARIA2_BT_MAX_OPEN_FILES", DefaultAria2BTMaxOpenFiles),
			MaxOverallUploadLimit:    getEnv("ARIA2_MAX_OVERALL_UPLOAD_LIMIT", "1M"),
			MaxUploadLimit:           getEnv("ARIA2_MAX_UPLOAD_LIMIT", "200K"),
			SeedRatio:                getEnvFloat("ARIA2_SEED_RATIO", 0.0),
			SeedTime:                 getEnvInt("ARIA2_SEED_TIME", 0),
			BTTrackerTimeout:         getEnvInt("ARIA2_BT_TRACKER_TIMEOUT", DefaultAria2BTTrackerTimeout),
			BTTrackerInterval:        getEnvInt("ARIA2_BT_TRACKER_INTERVAL", DefaultAria2BTTrackerInterval),
			EnableDHT:                getEnvBool("ARIA2_ENABLE_DHT", true),
			EnablePeerExchange:       getEnvBool("ARIA2_ENABLE_PEER_EXCHANGE", true),
			EnableLocalPeerDiscovery: getEnvBool("ARIA2_ENABLE_LOCAL_PEER_DISCOVERY", true),
			FollowTorrent:            getEnvBool("ARIA2_FOLLOW_TORRENT", true),
			ListenPort:               getEnv("ARIA2_LISTEN_PORT", "6881-6999"),
			DHTPorts:                 getEnv("ARIA2_DHT_PORTS", "6881-6999"),
			BTSaveMetadata:           getEnvBool("ARIA2_BT_SAVE_METADATA", true),
			BTHashCheckSeed:          getEnvBool("ARIA2_BT_HASH_CHECK_SEED", true),
			BTRequireCrypto:          getEnvBool("ARIA2_BT_REQUIRE_CRYPTO", false),
			BTMinCryptoLevel:         getEnv("ARIA2_BT_MIN_CRYPTO_LEVEL", "plain"),
			CheckIntegrity:           getEnvBool("ARIA2_CHECK_INTEGRITY", false),
			ContinueDownload:         getEnvBool("ARIA2_CONTINUE_DOWNLOAD", true),
			RemoteTime:               getEnvBool("ARIA2_REMOTE_TIME", true),
			FileAllocation:           getEnv("ARIA2_FILE_ALLOCATION", "none"),
			HTTPProxy:                getEnv("ARIA2_HTTP_PROXY", ""),
			AllProxy:                 getEnv("ARIA2_ALL_PROXY", ""),
			UserAgent:                getEnv("ARIA2_USER_AGENT", ""),
			Timeout:                  getEnvInt("ARIA2_TIMEOUT", DefaultAria2Timeout),
			MaxTries:                 getEnvInt("ARIA2_MAX_TRIES", DefaultAria2MaxTries),
			RetryWait:                getEnvInt("ARIA2_RETRY_WAIT", 0),
		},

		VideoSettings: VideoConfig{
			EnableReencoding:   getEnvBool("VIDEO_ENABLE_REENCODING", false),
			ForceReencoding:    getEnvBool("VIDEO_FORCE_REENCODING", false),
			VideoCodec:         getEnv("VIDEO_CODEC", "h264"),
			AudioCodec:         getEnv("AUDIO_CODEC", "mp3"),
			OutputFormat:       getEnv("VIDEO_OUTPUT_FORMAT", "mp4"),
			FFmpegExtraArgs:    getEnv("FFMPEG_EXTRA_ARGS", "-pix_fmt yuv420p"),
			QualitySelector:    getEnv("VIDEO_QUALITY_SELECTOR", "bv*+ba/b"),
			MaxHeight:          getEnvInt("VIDEO_MAX_HEIGHT", DefaultVideoMaxHeight),
			CompatibilityMode:  getEnvBool("VIDEO_COMPATIBILITY_MODE", false),
			RejectIncompatible: getEnvBool("VIDEO_REJECT_INCOMPATIBLE", false),
			SubtitleLang:       getEnv("VIDEO_SUBTITLE_LANG", ""),
			AudioLang:          getEnv("VIDEO_AUDIO_LANG", ""),
			WriteSubs:          getEnvBool("VIDEO_WRITE_SUBS", false),
			TvH264Level:        getEnv("VIDEO_TV_H264_LEVEL", "4.1"),
		},
	}

	if getEnv("RUNNING_IN_DOCKER", "false") == "true" {
		config.MoviePath = "/app/media"
		config.LangPath = "/app/locales"
		if logutils.Log != nil {
			logutils.Log.WithFields(map[string]any{
				"movie_path": config.MoviePath,
				"lang_path":  config.LangPath,
			}).Info("Running inside Docker, using container paths")
		}
	}

	if err := config.validate(); err != nil {
		if logutils.Log != nil {
			logutils.Log.WithError(err).Error("Configuration validation failed")
		}
		return nil, utils.WrapError(err, "configuration validation failed", nil)
	}
	if logutils.Log != nil {
		logutils.Log.Info("Configuration loaded successfully")
	}
	return config, nil
}

type Config struct {
	BotToken        string
	MoviePath       string
	AdminPassword   string
	RegularPassword string
	Lang            string
	TelegramProxy   string
	Proxy           string
	ProxyDomains    string
	LogLevel        string
	LangPath        string
	ProwlarrURL     string
	ProwlarrAPIKey  string
	TMSAPIEnabled   bool
	TMSAPIListen    string // e.g. "127.0.0.1:8080" or "0.0.0.0:8080"
	TMSAPIKey       string
	TMSWebhookURL   string // optional; POST on download completion/failure
	TMSWebhookToken string // optional; sent as Authorization: Bearer <token> when calling TMS_WEBHOOK_URL (e.g. for OpenClaw hooks)
	// TMSWebhookFormat: json|tms (default), openclaw_wake, openclaw_agent. Empty = auto from URL (/hooks/wake, /hooks/agent).
	TMSWebhookFormat       string
	YtdlpPath              string // Path to yt-dlp binary.
	YtdlpExtraArgs         string // Whitespace-separated arguments applied to every yt-dlp invocation.
	YtdlpCookiesPath       string // Optional Netscape-format cookies file used by yt-dlp.
	YtdlpUpdateMode        string // self: yt-dlp -U; pip: python -m pip install --upgrade yt-dlp; off: disabled
	YtdlpPythonPath        string // Python executable used when YtdlpUpdateMode is pip.
	YtdlpUpdateOnStart     bool
	YtdlpUpdateInterval    time.Duration
	QBittorrentURL         string // When set, torrents are handled by qBittorrent Web API instead of aria2 (e.g. http://localhost:8080)
	QBittorrentUsername    string
	QBittorrentPassword    string
	TorrentFallbackToAria2 bool // If true, qBittorrent setup/start errors can fall back to aria2.

	DownloadSettings DownloadConfig
	SecuritySettings SecurityConfig
	Aria2Settings    Aria2Config
	VideoSettings    VideoConfig
}

type DownloadConfig struct {
	MaxConcurrentDownloads int
	DownloadTimeout        time.Duration
	ProgressUpdateInterval time.Duration
}

type Aria2Config struct {
	MaxPeers                 int
	MaxConnectionsPerServer  int
	Split                    int
	MinSplitSize             string
	BTMaxPeers               int
	BTRequestPeerSpeedLimit  string
	BTMaxOpenFiles           int
	MaxOverallUploadLimit    string
	MaxUploadLimit           string
	SeedRatio                float64
	SeedTime                 int
	BTTrackerTimeout         int
	BTTrackerInterval        int
	EnableDHT                bool
	EnablePeerExchange       bool
	EnableLocalPeerDiscovery bool
	FollowTorrent            bool
	ListenPort               string
	DHTPorts                 string
	BTSaveMetadata           bool
	BTHashCheckSeed          bool
	BTRequireCrypto          bool
	BTMinCryptoLevel         string
	CheckIntegrity           bool
	ContinueDownload         bool
	RemoteTime               bool
	FileAllocation           string
	HTTPProxy                string
	AllProxy                 string
	UserAgent                string
	Timeout                  int
	MaxTries                 int
	RetryWait                int
}

type SecurityConfig struct {
	PasswordMinLength int
}

type VideoConfig struct {
	EnableReencoding   bool
	ForceReencoding    bool
	VideoCodec         string
	AudioCodec         string
	OutputFormat       string
	FFmpegExtraArgs    string
	QualitySelector    string
	MaxHeight          int // Maximum video height (e.g., 1080 for 1080p, 720 for 720p, 0 for no limit)
	CompatibilityMode  bool
	RejectIncompatible bool // if true, stop download and notify when video is red (not playable on TV)
	SubtitleLang       string
	AudioLang          string
	WriteSubs          bool
	TvH264Level        string // H.264 level cap for compatibility mode (e.g. "4.0", "4.1")
}

func (c *Config) GetDownloadSettings() DownloadConfig {
	return c.DownloadSettings
}

func (c *Config) GetSecuritySettings() SecurityConfig {
	return c.SecuritySettings
}

func (c *Config) GetAria2Settings() Aria2Config {
	return c.Aria2Settings
}

func (c *Config) GetVideoSettings() VideoConfig {
	return c.VideoSettings
}
