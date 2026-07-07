export interface SearchableBuyerAccount {
    accountId?: string
    accountName?: string
    uid?: string
}

export interface SearchableBuyer {
    logicalId: string
    name?: string
    tel?: string
    tels?: string[]
    idCard?: string
    accounts?: SearchableBuyerAccount[]
}

const COMMON_PINYIN_INITIALS: Record<string, string> = {
    // Common surnames and frequent name characters. This intentionally stays
    // small; raw Chinese character search still works for every name.
    赵: 'z', 钱: 'q', 孙: 's', 李: 'l', 周: 'z', 吴: 'w', 郑: 'z', 王: 'w',
    冯: 'f', 陈: 'c', 褚: 'c', 卫: 'w', 蒋: 'j', 沈: 's', 韩: 'h', 杨: 'y',
    朱: 'z', 秦: 'q', 尤: 'y', 许: 'x', 何: 'h', 吕: 'l', 施: 's', 张: 'z',
    孔: 'k', 曹: 'c', 严: 'y', 华: 'h', 金: 'j', 魏: 'w', 陶: 't', 姜: 'j',
    戚: 'q', 谢: 'x', 邹: 'z', 喻: 'y', 柏: 'b', 水: 's', 窦: 'd', 章: 'z',
    云: 'y', 苏: 's', 潘: 'p', 葛: 'g', 奚: 'x', 范: 'f', 彭: 'p', 郎: 'l',
    鲁: 'l', 韦: 'w', 昌: 'c', 马: 'm', 苗: 'm', 凤: 'f', 花: 'h', 方: 'f',
    俞: 'y', 任: 'r', 袁: 'y', 柳: 'l', 鲍: 'b', 史: 's', 唐: 't', 费: 'f',
    廉: 'l', 岑: 'c', 薛: 'x', 雷: 'l', 贺: 'h', 倪: 'n', 汤: 't', 滕: 't',
    殷: 'y', 罗: 'l', 毕: 'b', 郝: 'h', 邬: 'w', 安: 'a', 常: 'c', 乐: 'l',
    于: 'y', 时: 's', 傅: 'f', 皮: 'p', 卞: 'b', 齐: 'q', 康: 'k', 伍: 'w',
    余: 'y', 元: 'y', 卜: 'b', 顾: 'g', 孟: 'm', 平: 'p', 黄: 'h', 和: 'h',
    穆: 'm', 萧: 'x', 尹: 'y', 姚: 'y', 邵: 's', 湛: 'z', 汪: 'w', 祁: 'q',
    毛: 'm', 禹: 'y', 狄: 'd', 米: 'm', 贝: 'b', 明: 'm', 臧: 'z', 计: 'j',
    伏: 'f', 成: 'c', 戴: 'd', 谈: 't', 宋: 's', 庞: 'p', 熊: 'x', 纪: 'j',
    舒: 's', 屈: 'q', 项: 'x', 祝: 'z', 董: 'd', 梁: 'l', 杜: 'd', 阮: 'r',
    蓝: 'l', 闵: 'm', 席: 'x', 季: 'j', 麻: 'm', 强: 'q', 贾: 'j', 路: 'l',
    娄: 'l', 危: 'w', 江: 'j', 童: 't', 颜: 'y', 郭: 'g', 梅: 'm', 盛: 's',
    林: 'l', 刁: 'd', 钟: 'z', 徐: 'x', 邱: 'q', 骆: 'l', 高: 'g', 夏: 'x',
    蔡: 'c', 田: 't', 胡: 'h', 凌: 'l', 霍: 'h', 虞: 'y', 万: 'w', 支: 'z',
    柯: 'k', 昝: 'z', 管: 'g', 卢: 'l', 莫: 'm', 经: 'j', 房: 'f', 裘: 'q',
    缪: 'm', 干: 'g', 解: 'x', 应: 'y', 宗: 'z', 丁: 'd', 宣: 'x', 邓: 'd',
    郁: 'y', 单: 's', 杭: 'h', 洪: 'h', 包: 'b', 诸: 'z', 左: 'z', 石: 's',
    崔: 'c', 吉: 'j', 龚: 'g', 程: 'c', 邢: 'x', 裴: 'p', 陆: 'l', 荣: 'r',
    翁: 'w', 荀: 'x', 羊: 'y', 於: 'y', 惠: 'h', 甄: 'z', 曲: 'q', 家: 'j',
    封: 'f', 芮: 'r', 羿: 'y', 储: 'c', 靳: 'j', 汲: 'j', 邴: 'b', 糜: 'm',
    松: 's', 井: 'j', 段: 'd', 富: 'f', 巫: 'w', 乌: 'w', 焦: 'j', 巴: 'b',
    弓: 'g', 牧: 'm', 隗: 'k', 山: 's', 谷: 'g', 车: 'c', 侯: 'h', 宓: 'm',
    蓬: 'p', 全: 'q', 郗: 'x', 班: 'b', 仰: 'y', 秋: 'q', 仲: 'z', 伊: 'y',
    宫: 'g', 宁: 'n', 仇: 'q', 栾: 'l', 暴: 'b', 甘: 'g', 斜: 'x', 厉: 'l',
    戎: 'r', 祖: 'z', 武: 'w', 符: 'f', 刘: 'l', 景: 'j', 詹: 'z', 龙: 'l',
    叶: 'y', 幸: 'x', 司: 's', 黎: 'l', 溥: 'p', 印: 'y', 怀: 'h', 蒲: 'p',
    邰: 't', 从: 'c', 鄂: 'e', 索: 's', 咸: 'x', 籍: 'j', 赖: 'l', 卓: 'z',
    蔺: 'l', 屠: 't', 蒙: 'm', 池: 'c', 乔: 'q', 阳: 'y', 郁: 'y', 胥: 'x',
    能: 'n', 苍: 'c', 双: 's', 闻: 'w', 莘: 's', 党: 'd', 翟: 'z', 谭: 't',
    贡: 'g', 劳: 'l', 逄: 'p', 姬: 'j', 申: 's', 扶: 'f', 堵: 'd', 冉: 'r',
    宰: 'z', 郦: 'l', 雍: 'y', 却: 'q', 璩: 'q', 桑: 's', 桂: 'g', 濮: 'p',
    牛: 'n', 寿: 's', 通: 't', 边: 'b', 扈: 'h', 燕: 'y', 冀: 'j', 郏: 'j',
    浦: 'p', 尚: 's', 农: 'n', 温: 'w', 别: 'b', 庄: 'z', 晏: 'y', 柴: 'c',
    瞿: 'q', 阎: 'y', 连: 'l', 习: 'x', 容: 'r', 向: 'x', 古: 'g', 易: 'y',
    慎: 's', 戈: 'g', 廖: 'l', 庾: 'y', 终: 'z', 暨: 'j', 居: 'j', 衡: 'h',
    步: 'b', 都: 'd', 耿: 'g', 满: 'm', 弘: 'h', 匡: 'k', 国: 'g', 文: 'w',
    寇: 'k', 广: 'g', 禄: 'l', 阙: 'q', 东: 'd', 欧: 'o', 殳: 's', 沃: 'w',
    利: 'l', 蔚: 'w', 越: 'y', 夔: 'k', 隆: 'l', 师: 's', 巩: 'g', 厍: 's',
    聂: 'n', 晁: 'c', 勾: 'g', 敖: 'a', 融: 'r', 冷: 'l', 訾: 'z', 辛: 'x',
    阚: 'k', 那: 'n', 简: 'j', 饶: 'r', 空: 'k', 曾: 'z', 毋: 'w', 沙: 's',
    乜: 'n', 养: 'y', 鞠: 'j', 须: 'x', 丰: 'f', 巢: 'c', 关: 'g', 蒯: 'k',
    相: 'x', 查: 'z', 后: 'h', 荆: 'j', 红: 'h', 游: 'y', 竺: 'z', 权: 'q',
    逯: 'l', 盖: 'g', 益: 'y', 桓: 'h', 公: 'g',
}

function normalizeText(value: unknown) {
    return String(value ?? '').trim().toLowerCase()
}

function compactText(value: unknown) {
    return normalizeText(value).replace(/[\s\-_.·*()（）[\]【】/\\]/g, '')
}

export function buyerPinyinInitials(name: string) {
    return Array.from(name || '').map(ch => {
        if (/[a-z0-9]/i.test(ch)) return ch.toLowerCase()
        return COMMON_PINYIN_INITIALS[ch] || ''
    }).join('')
}

export function buyerIdTail(buyer: SearchableBuyer, size = 4) {
    const idCard = compactText(buyer.idCard)
    return idCard ? idCard.slice(-size) : ''
}

export function buyerAccountCount(buyer: SearchableBuyer) {
    return (buyer.accounts || []).length
}

export function buildBuyerSearchFields(buyer: SearchableBuyer) {
    const fields = [
        buyer.logicalId,
        buyer.name,
        buyerPinyinInitials(buyer.name || ''),
        buyer.tel,
        ...(buyer.tels || []),
        buyer.idCard,
        buyerIdTail(buyer),
    ]
    for (const account of buyer.accounts || []) {
        fields.push(account.accountId, account.accountName, account.uid)
    }
    return fields
        .map(field => [normalizeText(field), compactText(field)])
        .flat()
        .filter(Boolean)
}

export function buildBuyerSearchText(buyer: SearchableBuyer) {
    return buildBuyerSearchFields(buyer).join(' ')
}

export function matchesBuyerSearch(buyer: SearchableBuyer, keyword: string) {
    const tokens = compactText(keyword)
        ? normalizeText(keyword).split(/\s+/).filter(Boolean)
        : []
    if (tokens.length === 0) return true
    const fields = buildBuyerSearchFields(buyer)
    return tokens.every(token => {
        const normalized = normalizeText(token)
        const compacted = compactText(token)
        return fields.some(field => field.includes(normalized) || (!!compacted && field.includes(compacted)))
    })
}

export function filterBuyersBySearch<T extends SearchableBuyer>(buyers: T[], keyword: string): T[] {
    if (!normalizeText(keyword)) return buyers
    return buyers.filter(buyer => matchesBuyerSearch(buyer, keyword))
}
