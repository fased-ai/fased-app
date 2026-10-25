export const languages = {
  en: "English",
  ru: "Русский",
  es: "Español",
  zh: "简体中文",
  ko: "한국어",
} as const;
export type Language = keyof typeof languages;
// Interface copy only. Never translate user records, model output or server receipts.
const rows = {
  Chat: ["Чат", "Chat", "聊天", "채팅"],
  Tasks: ["Задачи", "Tareas", "任务", "작업"],
  Agents: ["Агенты", "Agentes", "智能体", "에이전트"],
  More: ["Ещё", "Más", "更多", "더 보기"],
  News: ["Новости", "Noticias", "资讯", "뉴스"],
  Mining: ["Майнинг", "Minería", "挖矿", "마이닝"],
  Onchain: ["Ончейн", "Onchain", "链上", "온체인"],
  Hyperliquid: ["Hyperliquid", "Hyperliquid", "Hyperliquid", "Hyperliquid"],
  Overview: ["Обзор", "Resumen", "概览", "개요"],
  Activity: ["Активность", "Actividad", "活动", "활동"],
  Wallets: ["Кошельки", "Billeteras", "钱包", "지갑"],
  Providers: ["Провайдеры", "Proveedores", "模型服务", "제공업체"],
  Settings: ["Настройки", "Configuración", "设置", "설정"],
  Preferences: ["Интерфейс", "Preferencias", "偏好设置", "환경설정"],
  Appearance: ["Оформление", "Apariencia", "外观", "화면 모드"],
  Language: ["Язык", "Idioma", "语言", "언어"],
  Light: ["Светлая", "Claro", "浅色", "라이트"],
  Dark: ["Тёмная", "Oscuro", "深色", "다크"],
  System: ["Системная", "Sistema", "跟随系统", "시스템"],
  "All settings": [
    "Все настройки",
    "Toda la configuración",
    "所有设置",
    "모든 설정",
  ],
  "Hide navigation rail": [
    "Скрыть панель навигации",
    "Ocultar barra lateral",
    "隐藏侧栏",
    "탐색 모음 숨기기",
  ],
  "Show navigation rail": [
    "Показать панель навигации",
    "Mostrar barra lateral",
    "显示侧栏",
    "탐색 모음 표시",
  ],
  "Main navigation": [
    "Основная навигация",
    "Navegación principal",
    "主导航",
    "기본 탐색",
  ],
  "Page shortcuts": [
    "Быстрый переход",
    "Accesos a páginas",
    "页面快捷方式",
    "페이지 바로가기",
  ],
  "More pages": ["Другие разделы", "Más páginas", "更多页面", "다른 페이지"],
  "Search Fased": [
    "Поиск в Fased",
    "Buscar en Fased",
    "搜索 Fased",
    "Fased 검색",
  ],
  "Search workspace": [
    "Поиск по разделам",
    "Buscar en el espacio",
    "搜索工作区",
    "작업 공간 검색",
  ],
  "No matching workspace.": [
    "Ничего не найдено.",
    "No hay resultados.",
    "没有匹配结果。",
    "검색 결과가 없습니다.",
  ],
  Notifications: ["Уведомления", "Notificaciones", "通知", "알림"],
  unread: ["непрочитанных", "sin leer", "未读", "읽지 않음"],
  Local: ["Локально", "Local", "本地", "로컬"],
  Remote: ["Удалённо", "Remoto", "远程", "원격"],
  Preview: ["Предпросмотр", "Vista previa", "预览", "미리보기"],
  "Instance location": [
    "Подключённый экземпляр",
    "Ubicación de la instancia",
    "实例位置",
    "인스턴스 위치",
  ],
  "Sign out": ["Выйти", "Cerrar sesión", "退出登录", "로그아웃"],
  "Skip to workspace": [
    "Перейти к рабочей области",
    "Ir al espacio de trabajo",
    "跳转到工作区",
    "작업 공간으로 건너뛰기",
  ],
  "Close dialog": [
    "Закрыть окно",
    "Cerrar diálogo",
    "关闭对话框",
    "대화 상자 닫기",
  ],
  "Open context panel": [
    "Открыть контекст",
    "Abrir contexto",
    "打开上下文",
    "컨텍스트 열기",
  ],
  "Close context panel": [
    "Закрыть контекст",
    "Cerrar contexto",
    "关闭上下文",
    "컨텍스트 닫기",
  ],
  "Workspace context": ["Контекст", "Contexto", "上下文", "컨텍스트"],
  Memory: ["Память", "Memoria", "记忆", "메모리"],
  "Inspect memory": [
    "Открыть память",
    "Ver memoria",
    "查看记忆",
    "메모리 보기",
  ],
  Agent: ["Агент", "Agente", "智能体", "에이전트"],
  "New profile": ["Новый профиль", "Nuevo perfil", "新建配置", "새 프로필"],
  "New task": ["Новая задача", "Nueva tarea", "新建任务", "새 작업"],
  "Review task": ["Проверить задачу", "Revisar tarea", "审核任务", "작업 검토"],
  "Create task": ["Создать задачу", "Crear tarea", "创建任务", "작업 생성"],
  "Task name": [
    "Название задачи",
    "Nombre de la tarea",
    "任务名称",
    "작업 이름",
  ],
  "Reminder text": [
    "Текст напоминания",
    "Texto del recordatorio",
    "提醒内容",
    "알림 내용",
  ],
  Schedule: ["Расписание", "Horario", "计划", "일정"],
  "Run limit": [
    "Лимит запусков",
    "Límite de ejecuciones",
    "运行次数上限",
    "실행 한도",
  ],
  "Run now": ["Запустить", "Ejecutar ahora", "立即运行", "지금 실행"],
  Pause: ["Приостановить", "Pausar", "暂停", "일시 중지"],
  Resume: ["Продолжить", "Reanudar", "恢复", "재개"],
  Cancel: ["Отмена", "Cancelar", "取消", "취소"],
  Save: ["Сохранить", "Guardar", "保存", "저장"],
  Edit: ["Изменить", "Editar", "编辑", "편집"],
  Result: ["Результат", "Resultado", "结果", "결과"],
  Sources: ["Источники", "Fuentes", "来源", "출처"],
  "New conversation": [
    "Новый диалог",
    "Nueva conversación",
    "新建对话",
    "새 대화",
  ],
  "Conversation name": [
    "Название диалога",
    "Nombre de la conversación",
    "对话名称",
    "대화 이름",
  ],
  "Create conversation": [
    "Создать диалог",
    "Crear conversación",
    "创建对话",
    "대화 생성",
  ],
  "Search conversations": [
    "Поиск диалогов",
    "Buscar conversaciones",
    "搜索对话",
    "대화 검색",
  ],
  "Start chat": ["Начать чат", "Iniciar chat", "开始聊天", "채팅 시작"],
  Message: ["Сообщение", "Mensaje", "消息", "메시지"],
  Send: ["Отправить", "Enviar", "发送", "보내기"],
  Stop: ["Остановить", "Detener", "停止", "중지"],
  "Save message": [
    "Сохранить сообщение",
    "Guardar mensaje",
    "保存消息",
    "메시지 저장",
  ],
  "Remember this": ["Запомнить", "Recordar esto", "记住这条", "기억하기"],
  "Preferences cannot be saved in this browser.": [
    "Браузер не сохраняет настройки интерфейса.",
    "Este navegador no puede guardar las preferencias.",
    "此浏览器无法保存偏好设置。",
    "이 브라우저에서 환경설정을 저장할 수 없습니다.",
  ],
} satisfies Record<string, [string, string, string, string]>;
export type Copy = keyof typeof rows;
export const copyKeys = Object.keys(rows) as Copy[];
export function translate(language: Language, key: string): string {
  const row = rows[key as Copy];
  return language === "en" || !row
    ? key
    : row[({ ru: 0, es: 1, zh: 2, ko: 3 } as const)[language]];
}
export function resolveLanguage(
  value: string,
  browser: readonly string[],
): Language {
  if (Object.hasOwn(languages, value)) return value as Language;
  if (value === "system")
    for (const locale of browser) {
      const code = locale.toLowerCase().split(/[-_]/)[0];
      if (Object.hasOwn(languages, code)) return code as Language;
    }
  return "en";
}
