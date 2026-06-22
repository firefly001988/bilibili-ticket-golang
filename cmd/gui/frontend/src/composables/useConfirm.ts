import { reactive } from 'vue'

interface ConfirmState {
    show: boolean
    title: string
    message: string
    resolve: (v: boolean) => void
}

const state = reactive<ConfirmState>({
    show: false,
    title: '',
    message: '',
    resolve: () => { }
})

// 全局 store，供 ConfirmDialog 组件绑定
export function useConfirmStore() {
    return {
        state, resolve: (v: boolean) => {
            state.resolve(v)
            state.show = false
        }
    }
}

// 页面直接调用的 JS 方法 —— 不需要任何模版代码
export function useConfirm() {
    return {
        show(title: string, message: string): Promise<boolean> {
            return new Promise(resolve => {
                state.title = title
                state.message = message
                state.resolve = resolve
                state.show = true
            })
        }
    }
}
