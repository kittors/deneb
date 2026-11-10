package control

import "strings"

// A relay registering with a join code says what it could find out about
// where it is (proto.RelayInfo.Region), and its card is named after the
// place: "我的服务器（香港）", with nothing for its owner to fill in. The
// table lives here, not in the relay program, so a new region needs no
// relay to be updated. A clue the table does not know gives no name, and
// the relay is named after its address.

// regionName is the place a region clue stands for, as people say it, or
// "".
func regionName(clue string) string {
	kind, v, ok := strings.Cut(strings.TrimSpace(clue), ":")
	if !ok {
		return ""
	}
	if kind == "name" {
		// The relay's own -region: taken as it is, if it is a name.
		n, err := checkName(v)
		if err != nil {
			return ""
		}
		return n
	}
	v = strings.ToLower(strings.TrimSpace(v))
	if kind == "gcp" {
		// A zone, "asia-east2-a", or its region.
		if i := strings.LastIndexByte(v, '-'); i > 0 && len(v)-i == 2 {
			v = v[:i]
		}
	}
	if kind == "tz" {
		// Time zones are cased: Asia/Hong_Kong.
		return regionNames[kind][strings.TrimSpace(clue[len("tz:"):])]
	}
	return regionNames[kind][v]
}

var regionNames = map[string]map[string]string{
	"aliyun": {
		"cn-hongkong": "香港", "cn-hangzhou": "杭州", "cn-shanghai": "上海", "cn-beijing": "北京",
		"cn-shenzhen": "深圳", "cn-guangzhou": "广州", "cn-chengdu": "成都", "cn-qingdao": "青岛",
		"cn-zhangjiakou": "张家口", "cn-huhehaote": "呼和浩特", "cn-wulanchabu": "乌兰察布", "cn-heyuan": "河源",
		"cn-nanjing": "南京", "cn-fuzhou": "福州", "ap-northeast-1": "东京", "ap-northeast-2": "首尔",
		"ap-southeast-1": "新加坡", "ap-southeast-3": "吉隆坡", "ap-southeast-5": "雅加达", "ap-southeast-6": "马尼拉",
		"ap-southeast-7": "曼谷", "us-west-1": "硅谷", "us-east-1": "弗吉尼亚", "eu-central-1": "法兰克福",
		"eu-west-1": "伦敦", "me-east-1": "迪拜",
	},
	"tencent": {
		"ap-hongkong": "香港", "ap-guangzhou": "广州", "ap-shanghai": "上海", "ap-beijing": "北京",
		"ap-chengdu": "成都", "ap-chongqing": "重庆", "ap-nanjing": "南京", "ap-tokyo": "东京",
		"ap-seoul": "首尔", "ap-singapore": "新加坡", "ap-bangkok": "曼谷", "ap-jakarta": "雅加达",
		"ap-mumbai": "孟买", "na-siliconvalley": "硅谷", "na-ashburn": "弗吉尼亚", "na-toronto": "多伦多",
		"eu-frankfurt": "法兰克福", "sa-saopaulo": "圣保罗",
	},
	"aws": {
		"ap-east-1": "香港", "ap-east-2": "台北", "ap-northeast-1": "东京", "ap-northeast-2": "首尔",
		"ap-northeast-3": "大阪", "ap-southeast-1": "新加坡", "ap-southeast-2": "悉尼", "ap-southeast-3": "雅加达",
		"ap-southeast-5": "吉隆坡", "ap-south-1": "孟买", "us-east-1": "弗吉尼亚", "us-east-2": "俄亥俄",
		"us-west-1": "加州", "us-west-2": "俄勒冈", "ca-central-1": "加拿大", "eu-central-1": "法兰克福",
		"eu-west-1": "爱尔兰", "eu-west-2": "伦敦", "eu-west-3": "巴黎", "eu-north-1": "斯德哥尔摩",
		"sa-east-1": "圣保罗", "me-central-1": "阿联酋", "cn-north-1": "北京", "cn-northwest-1": "宁夏",
	},
	"gcp": {
		"asia-east1": "台湾", "asia-east2": "香港", "asia-northeast1": "东京", "asia-northeast2": "大阪",
		"asia-northeast3": "首尔", "asia-southeast1": "新加坡", "asia-southeast2": "雅加达", "asia-south1": "孟买",
		"australia-southeast1": "悉尼", "us-west1": "俄勒冈", "us-west2": "洛杉矶", "us-central1": "爱荷华",
		"us-east1": "南卡罗来纳", "us-east4": "弗吉尼亚", "europe-west1": "比利时", "europe-west2": "伦敦",
		"europe-west3": "法兰克福", "europe-west4": "荷兰",
	},
	"azure": {
		"eastasia": "香港", "southeastasia": "新加坡", "japaneast": "东京", "japanwest": "大阪",
		"koreacentral": "首尔", "westus": "加州", "westus2": "华盛顿州", "eastus": "弗吉尼亚",
		"centralus": "爱荷华", "northeurope": "爱尔兰", "westeurope": "荷兰", "uksouth": "伦敦",
		"germanywestcentral": "法兰克福", "australiaeast": "悉尼", "chinaeast2": "上海", "chinanorth3": "河北",
	},
	"tz": {
		"Asia/Hong_Kong": "香港", "Asia/Macau": "澳门", "Asia/Taipei": "台北", "Asia/Shanghai": "中国",
		"Asia/Chongqing": "中国", "Asia/Urumqi": "新疆", "Asia/Tokyo": "东京", "Asia/Seoul": "首尔",
		"Asia/Singapore": "新加坡", "Asia/Kuala_Lumpur": "吉隆坡", "Asia/Bangkok": "曼谷", "Asia/Jakarta": "雅加达",
		"Asia/Manila": "马尼拉", "Asia/Ho_Chi_Minh": "胡志明市", "Asia/Kolkata": "印度", "Asia/Dubai": "迪拜",
		"Europe/London": "伦敦", "Europe/Paris": "巴黎", "Europe/Berlin": "德国", "Europe/Amsterdam": "阿姆斯特丹",
		"Europe/Moscow": "莫斯科", "America/Los_Angeles": "美国西部", "America/New_York": "美国东部",
		"America/Chicago": "美国中部", "America/Toronto": "多伦多", "America/Sao_Paulo": "圣保罗",
		"Australia/Sydney": "悉尼",
	},
}
