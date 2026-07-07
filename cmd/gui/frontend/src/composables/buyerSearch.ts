import { pinyin } from 'pinyin-pro'

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

function normalizeText(value: unknown) {
    return String(value ?? '').trim().toLowerCase()
}

function compactText(value: unknown) {
    return normalizeText(value).replace(/[\s\-_.·*()（）[\]【】/\\'"‘’“”]/g, '')
}

function uniqueFields(fields: Array<string | undefined>) {
    return Array.from(new Set(fields.map(field => compactText(field)).filter(Boolean)))
}

export function buyerPinyinTokens(name: string) {
    const normalizedName = normalizeText(name)
    if (!normalizedName) return []
    return pinyin(normalizedName, {
        toneType: 'none',
        type: 'array',
        mode: 'surname',
        surname: 'head',
        nonZh: 'consecutive',
        v: true,
    }).map(token => compactText(token)).filter(Boolean)
}

export function buyerPinyinInitials(name: string) {
    const normalizedName = normalizeText(name)
    if (!normalizedName) return ''
    return pinyin(normalizedName, {
        toneType: 'none',
        type: 'array',
        pattern: 'first',
        mode: 'surname',
        surname: 'head',
        nonZh: 'consecutive',
        v: true,
    }).map(token => compactText(token)).join('')
}

export function buyerIdTail(buyer: SearchableBuyer, size = 4) {
    const idCard = compactText(buyer.idCard)
    return idCard ? idCard.slice(-size) : ''
}

export function buyerAccountCount(buyer: SearchableBuyer) {
    return (buyer.accounts || []).length
}

export function buildBuyerSearchFields(buyer: SearchableBuyer) {
    const pinyinTokens = buyerPinyinTokens(buyer.name || '')
    const pinyinInitials = buyerPinyinInitials(buyer.name || '')
    const fields: Array<string | undefined> = [
        buyer.logicalId,
        buyer.name,
        pinyinInitials,
        pinyinTokens.join(''),
        pinyinTokens.join(' '),
        ...pinyinTokens,
        buyer.tel,
        ...(buyer.tels || []),
        buyer.idCard,
        buyerIdTail(buyer),
    ]
    for (const account of buyer.accounts || []) {
        fields.push(account.accountId, account.accountName, account.uid)
    }
    return uniqueFields(fields).flatMap(field => [field, normalizeText(field)])
}

export function buildBuyerSearchText(buyer: SearchableBuyer) {
    return buildBuyerSearchFields(buyer).join(' ')
}

export function matchesBuyerSearch(buyer: SearchableBuyer, keyword: string) {
    const tokens = normalizeText(keyword)
        ? normalizeText(keyword).split(/\s+/).map(token => compactText(token)).filter(Boolean)
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
