package constants

// 错误码集中维护；错误 message 由各 service/handler 手动拼接（含实体名/字段名/角色名）。
const (
	CodeOK               = 0
	CodeBadRequest       = 1000
	CodeUnauthorized     = 1001
	CodeForbidden        = 1002
	CodeNotFound         = 1003
	CodeConflict         = 1004
	CodeValidationFailed = 1005
	CodeRateLimited      = 1006
	CodeInternalError    = 5000

	// 业务错误码
	CodePlotNotAvailable     = 2001
	CodePlanStateNotAllowed  = 2002
	CodeCropNotInSeason      = 2003
	CodePlanAlreadyCompleted = 2004
	CodeHarvestBeforeMature  = 2005
	CodePostRemoved          = 2006
	CodeDuplicateUsername    = 2007
	CodeInvalidCredentials   = 2008
	CodeUserDisabled         = 2009

	// 候补与递补业务错误码
	CodeWaitlistNotAllowed       = 2010 // 地块当前状态不允许登记候补
	CodeWaitlistDuplicate        = 2011 // 同一人同一地块已有有效候补
	CodeWaitlistNotFound         = 2012 // 候补记录不存在
	CodeWaitlistNotInvited       = 2013 // 未轮到该用户确认（非队首/未受邀）
	CodeWaitlistConfirmExpired   = 2014 // 确认已逾时，资格已顺延
	CodePlotConfirming           = 2015 // 地块处于候补确认期，不可被他人认养
	CodeWaitlistAlreadyProcessed = 2016 // 候补记录已处理，不能重复操作
)

// ErrorText 错误码默认文案（service/handler 可覆盖拼接更具体的 message）
var ErrorText = map[int]string{
	CodeOK:                       "ok",
	CodeBadRequest:               "请求参数错误",
	CodeUnauthorized:             "未登录或登录已过期",
	CodeForbidden:                "无权访问该资源",
	CodeNotFound:                 "资源不存在",
	CodeConflict:                 "资源状态冲突",
	CodeValidationFailed:         "参数校验失败",
	CodeRateLimited:              "请求过于频繁，请稍后再试",
	CodeInternalError:            "服务器内部错误",
	CodePlotNotAvailable:         "地块当前不可认养",
	CodePlanStateNotAllowed:      "种植计划当前状态不允许该操作",
	CodeCropNotInSeason:          "所选作物不在当前季节推荐列表",
	CodePlanAlreadyCompleted:     "种植计划已完成，无法再次操作",
	CodeHarvestBeforeMature:      "作物尚未成熟，不允许记录收成",
	CodePostRemoved:              "帖子已删除",
	CodeDuplicateUsername:        "用户名已被占用",
	CodeInvalidCredentials:       "用户名或密码错误",
	CodeUserDisabled:             "账号已被禁用",
	CodeWaitlistNotAllowed:       "地块当前状态不允许登记候补",
	CodeWaitlistDuplicate:        "你已在该地块的候补队列中，请勿重复登记",
	CodeWaitlistNotFound:         "候补记录不存在",
	CodeWaitlistNotInvited:       "尚未轮到你确认，请等待队首递补",
	CodeWaitlistConfirmExpired:   "确认时间已过，候补资格已自动顺延",
	CodePlotConfirming:           "地块正处于候补确认期，暂不可认养",
	CodeWaitlistAlreadyProcessed: "候补记录已处理，不能重复操作",
}
