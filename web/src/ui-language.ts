import { priorityCopy } from "./ui-language-priority";
import { pageCopy } from "./ui-language-pages";
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
  ...pageCopy,
  ...priorityCopy,
  "No expiry": ["Без срока", "Sin caducidad", "无有效期限制", "만료 없음"],
  "Weekdays at {time}": [
    "По будням в {time}",
    "Días laborables a las {time}",
    "工作日 {time}",
    "평일 {time}",
  ],
  "Daily at {time}": [
    "Ежедневно в {time}",
    "Cada día a las {time}",
    "每天 {time}",
    "매일 {time}",
  ],
  "Older memory revisions": [
    "Предыдущие версии",
    "Versiones anteriores",
    "更早的记忆版本",
    "이전 메모리 버전",
  ],
  "Inspect source": ["Открыть источник", "Ver fuente", "查看来源", "출처 보기"],
  "Delete note": ["Удалить заметку", "Eliminar nota", "删除笔记", "메모 삭제"],
  "Correct note": [
    "Исправить заметку",
    "Corregir nota",
    "修改笔记",
    "메모 수정",
  ],
  "Source & expiry": [
    "Источник и срок",
    "Fuente y caducidad",
    "来源与有效期",
    "출처 및 만료",
  ],
  "Save restored memory": [
    "Сохранить восстановленную заметку",
    "Guardar memoria restaurada",
    "保存恢复的记忆",
    "복원한 메모리 저장",
  ],
  "Save correction": [
    "Сохранить исправление",
    "Guardar corrección",
    "保存修改",
    "수정 저장",
  ],
  "Save memory": [
    "Сохранить заметку",
    "Guardar memoria",
    "保存记忆",
    "메모리 저장",
  ],
  "Restore a memory": [
    "Восстановить заметку",
    "Restaurar memoria",
    "恢复记忆",
    "메모리 복원",
  ],
  "Correct a memory": [
    "Исправить заметку",
    "Corregir memoria",
    "修改记忆",
    "메모리 수정",
  ],
  "Memory note": ["Заметка", "Nota de memoria", "记忆内容", "메모 내용"],
  "New memory": ["Новая заметка", "Nueva memoria", "新建记忆", "새 메모리"],
  "Relevant notes and recent chat": [
    "Подходящие заметки и недавний диалог",
    "Notas relevantes y chat reciente",
    "相关记忆和最近对话",
    "관련 메모와 최근 대화",
  ],
  Remember: ["Запомнить", "Recordar", "记住", "기억하기"],
  "Remember a fact": [
    "Запомнить факт",
    "Recordar un dato",
    "记住一条信息",
    "정보 기억하기",
  ],
  Receipts: ["Записи", "Registros", "记录", "기록"],
  Status: ["Состояние", "Estado", "状态", "상태"],
  messages: ["сообщений", "mensajes", "条消息", "개 메시지"],
  runs: ["запусков", "ejecuciones", "次运行", "회 실행"],
  Runs: ["Запуски", "Ejecuciones", "运行次数", "실행 횟수"],
  Archived: ["В архиве", "Archivado", "已归档", "보관됨"],
  "Export JSON": [
    "Экспорт JSON",
    "Exportar JSON",
    "导出 JSON",
    "JSON 내보내기",
  ],
  "Load more": ["Загрузить ещё", "Cargar más", "加载更多", "더 보기"],
  "Loading…": ["Загрузка…", "Cargando…", "加载中…", "불러오는 중…"],
  Search: ["Поиск", "Buscar", "搜索", "검색"],
  "Search archive": [
    "Поиск в архиве",
    "Buscar en el archivo",
    "搜索归档",
    "보관함 검색",
  ],
  "Review revision": [
    "Просмотреть версию",
    "Revisar versión",
    "查看历史版本",
    "이전 버전 검토",
  ],
  "Memory history": [
    "История памяти",
    "Historial de memoria",
    "记忆历史",
    "메모리 기록",
  ],
  "No archived conversations.": [
    "Архив диалогов пуст.",
    "No hay conversaciones archivadas.",
    "暂无归档对话。",
    "보관된 대화가 없습니다.",
  ],
  "No archived tasks.": [
    "Архив задач пуст.",
    "No hay tareas archivadas.",
    "暂无归档任务。",
    "보관된 작업이 없습니다.",
  ],
  "No earlier revisions.": [
    "Нет предыдущих версий.",
    "No hay versiones anteriores.",
    "暂无历史版本。",
    "이전 버전이 없습니다.",
  ],
  "Archived task result": [
    "Результат из архива",
    "Resultado archivado",
    "归档结果",
    "보관된 결과",
  ],
  "Automatic archiving keeps unread results and linked evidence active. Archived records remain available.":
    [
      "Непрочитанные результаты и связанные источники остаются активными. Архив доступен для просмотра.",
      "Los resultados no leídos y las fuentes vinculadas se conservan activos. El archivo sigue disponible.",
      "未读结果和关联来源保留在活动记录中。归档记录仍可查看。",
      "읽지 않은 결과와 연결된 근거는 활성 상태로 유지됩니다. 보관된 기록은 계속 볼 수 있습니다.",
    ],
  "days after completion": [
    "дней после завершения",
    "días después de completarse",
    "天后归档",
    "일 후",
  ],
  Manually: ["Вручную", "Manualmente", "手动", "수동"],
  "Archive completed reminders": [
    "Архивировать завершённые напоминания",
    "Archivar recordatorios completados",
    "归档已完成的提醒",
    "완료된 알림 보관",
  ],
  "History settings": [
    "Настройки истории",
    "Ajustes del historial",
    "历史设置",
    "기록 설정",
  ],
  "Archive conversation": [
    "Архивировать диалог",
    "Archivar conversación",
    "归档对话",
    "대화 보관",
  ],
  "Archived conversations": [
    "Архив диалогов",
    "Conversaciones archivadas",
    "已归档对话",
    "보관된 대화",
  ],
  "Archived tasks": [
    "Архив задач",
    "Tareas archivadas",
    "已归档任务",
    "보관된 작업",
  ],
  "Archive task": [
    "Архивировать задачу",
    "Archivar tarea",
    "归档任务",
    "작업 보관",
  ],
  Saturday: ["Суббота", "Sábado", "星期六", "토요일"],
  Friday: ["Пятница", "Viernes", "星期五", "금요일"],
  Thursday: ["Четверг", "Jueves", "星期四", "목요일"],
  Wednesday: ["Среда", "Miércoles", "星期三", "수요일"],
  Tuesday: ["Вторник", "Martes", "星期二", "화요일"],
  Monday: ["Понедельник", "Lunes", "星期一", "월요일"],
  Sunday: ["Воскресенье", "Domingo", "星期日", "일요일"],
  "Missing times skipped; repeated times run once.": [
    "Несуществующее время пропускается; повторяющееся выполняется один раз.",
    "Las horas inexistentes se omiten; las repetidas se ejecutan una vez.",
    "跳过不存在的时间；重复时间仅运行一次。",
    "존재하지 않는 시간은 건너뛰고 중복 시간에는 한 번만 실행합니다.",
  ],
  "Clock changes": [
    "Перевод часов",
    "Cambio de hora",
    "时钟调整",
    "일광 절약 시간 변경",
  ],
  "Recurring calendar": [
    "По календарю",
    "Calendario recurrente",
    "按日历重复",
    "달력 반복 일정",
  ],
  "End date": ["Дата окончания", "Fecha de fin", "结束日期", "종료 일시"],
  "Time zone": ["Часовой пояс", "Zona horaria", "时区", "시간대"],
  Day: ["День", "Día", "日期", "요일"],
  Time: ["Время", "Hora", "时间", "시간"],
  "Cron rule": ["Правило cron", "Regla cron", "Cron 规则", "Cron 규칙"],
  "Custom cron": [
    "Свой cron",
    "Cron personalizado",
    "自定义 cron",
    "사용자 지정 cron",
  ],
  Weekly: ["Еженедельно", "Semanalmente", "每周", "매주"],
  Weekdays: ["По будням", "Días laborables", "工作日", "평일"],
  Daily: ["Ежедневно", "Diariamente", "每天", "매일"],
  Recurrence: ["Повторение", "Recurrencia", "重复规则", "반복 일정"],
  Repeat: ["Повторять", "Repetir", "重复", "반복"],
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
export function translate(
  language: Language,
  key: string,
  values: Record<string, string | number> = {},
): string {
  const normalized = key.trim();
  const row = Object.hasOwn(rows, normalized)
    ? rows[normalized as Copy]
    : undefined;
  const result =
    language === "en" || !row
      ? key
      : key.slice(0, key.indexOf(normalized)) +
        row[({ ru: 0, es: 1, zh: 2, ko: 3 } as const)[language]] +
        key.slice(key.indexOf(normalized) + normalized.length);
  return result.replace(/\{(\w+)\}/g, (match, name: string) =>
    Object.hasOwn(values, name) ? String(values[name]) : match,
  );
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
