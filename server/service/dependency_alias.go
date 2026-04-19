package service

import (
	"encoding/json"
	"strings"
)

var pythonModulePackageAliases = map[string]string{
	"attr":       "attrs",
	"bs4":        "beautifulsoup4",
	"crypto":     "pycryptodome",
	"cryptodome": "pycryptodomex",
	"cv2":        "opencv-python",
	"dateutil":   "python-dateutil",
	"dotenv":     "python-dotenv",
	"execjs":     "pyexecjs",
	"jwt":        "pyjwt",
	"nacl":       "pynacl",
	"openssl":    "pyopenssl",
	"pil":        "pillow",
	"serial":     "pyserial",
	"sklearn":    "scikit-learn",
	"socks":      "pysocks",
	"websocket":  "websocket-client",
	"yaml":       "pyyaml",
}

func ResolvePythonAutoInstallPackage(moduleName string) string {
	moduleName = strings.TrimSpace(moduleName)
	if moduleName == "" {
		return ""
	}

	if mapped, exists := pythonModulePackageAliases[strings.ToLower(moduleName)]; exists {
		return mapped
	}

	return moduleName
}

func PythonAutoInstallAliases() map[string]string {
	aliases := make(map[string]string, len(pythonModulePackageAliases))
	for key, value := range pythonModulePackageAliases {
		aliases[key] = value
	}
	return aliases
}

func EncodePythonAutoInstallAliases() string {
	data, err := json.Marshal(PythonAutoInstallAliases())
	if err != nil {
		return "{}"
	}
	return string(data)
}
