export const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080/api/v1";

export const SESSION_COOKIE_NAME = "session";

export const ITEMS_PER_PAGE = 20;

// Описание записи ТО, когда механик оставил поле пустым: бэкенд пустое не
// принимает (service/maintenance.go). Канон, а НЕ t(...): строка уходит в
// свободнотекстовую колонку и остаётся там навсегда, так что перевод сделал бы
// одно и то же плановое ТО «Плановым ТО», «Scheduled maintenance» и «Жоспарлы
// ТҚ» — смотря какой язык интерфейса был у того, кто нажал кнопку.
export const DEFAULT_MAINTENANCE_DESCRIPTION = "Плановое ТО";

// Page-size options for lists with a per-user size selector (drivers, vehicles).
// The 1000 ceiling mirrors the backend cap (pkg.ListPageSizeMax); those lists
// virtualize rows so large pages stay smooth. Their default is intentionally
// higher than ITEMS_PER_PAGE — they are the heavy lists the selector targets.
export const PAGE_SIZE_OPTIONS = [25, 50, 100, 200, 500, 1000] as const;
export const DEFAULT_PAGE_SIZE = 50;

// Ceiling for lists whose endpoint still uses pkg.NewPagination — the backend's
// DefaultMaxPageSize. Those endpoints clamp a larger page_size SILENTLY (no
// 400), so offering 200+ there would render 100 rows under a «1–500 из N»
// label. Pass this as the `max` of usePageSize/PageSizeSelect until the
// endpoint opts into pkg.ListPageSizeMax the way drivers and vehicles did.
export const DEFAULT_MAX_PAGE_SIZE = 100;

export const SELECT_PAGE_SIZE = "50000";

// Per-browser UI scale (the A-/A+ control + /settings/appearance). Applied via
// the CSS `zoom` property on <html> — that mimics Chrome's Ctrl+zoom: it scales
// px and rem alike, reflows layout, and because <body> is a descendant it also
// scales portal-rendered dialogs/popovers/toasts. Stored in localStorage under
// ZOOM_STORAGE_KEY; the pre-hydration script in app/layout.tsx applies it before
// paint (no FOUC). If you change ZOOM_STORAGE_KEY, update that script too.
export const ZOOM_OPTIONS = [80, 90, 100, 110, 125, 150] as const;
export const DEFAULT_ZOOM = 100;
export const ZOOM_STORAGE_KEY = "ui:zoom";

// Kazakhstan ИИН — exactly 12 digits. Shared by the create form (zod schema),
// the IIN-check launchers, and the blacklist gate so the format rule lives once.
// No `g` flag, so `.test()` is stateless and safe to share.
export const IIN_REGEX = /^\d{12}$/;

// Мягкая маска номера ВУ: латиница+цифры, ≥1 цифра, длина 5–16. Покрывает
// реальные прод-форматы (КЗ «2 буквы+цифры», чисто цифровые, иностранные) и
// отбивает ФИО/кириллицу/пробелы/мусор. Создание проверяет через zod; правка —
// только при изменении поля (чтобы не блокировать ~1100 legacy-номеров). No `g`
// flag → `.test()` stateless, безопасно шарить.
// Потолок доверенных контактов на одного водителя. Держится и здесь (гасит
// кнопку «+»), и на бэкенде (400 на четвёртом): UI-ограничение прямой вызов
// эндпоинта не остановит.
export const TRUSTED_CONTACTS_MAX = 3;

export const LICENSE_NUMBER_REGEX = /^(?=.*\d)[A-Za-z0-9]{5,16}$/;

export const PERMISSIONS = {
  USERS_VIEW: "users:view",
  USERS_CREATE: "users:create",
  USERS_EDIT: "users:edit",
  USERS_DELETE: "users:delete",
  ROLES_VIEW: "roles:view",
  ROLES_CREATE: "roles:create",
  ROLES_EDIT: "roles:edit",
  ROLES_DELETE: "roles:delete",
  SETTINGS_VIEW: "settings:view",
  SETTINGS_EDIT: "settings:edit",
  AUDIT_VIEW: "audit:view",
  SESSIONS_VIEW: "sessions:view",
  PARKS_VIEW: "parks:view",
  PARKS_CREATE: "parks:create",
  PARKS_EDIT: "parks:edit",
  PARKS_DELETE: "parks:delete",
  WORK_RULES_VIEW: "work_rules:view",
  WORK_RULES_EDIT: "work_rules:edit",
  CARS_VIEW: "cars:view",
  CARS_CREATE: "cars:create",
  CARS_EDIT: "cars:edit",
  CARS_DELETE: "cars:delete",
  DIRECTORIES_VIEW: "directories:view",
  DIRECTORIES_CREATE: "directories:create",
  DIRECTORIES_EDIT: "directories:edit",
  DIRECTORIES_DELETE: "directories:delete",
  WAREHOUSES_VIEW: "warehouses:view",
  WAREHOUSES_CREATE: "warehouses:create",
  WAREHOUSES_EDIT: "warehouses:edit",
  WAREHOUSES_DELETE: "warehouses:delete",
  DRIVERS_VIEW: "drivers:view",
  DRIVERS_CREATE: "drivers:create",
  DRIVERS_EDIT: "drivers:edit",
  DRIVERS_DELETE: "drivers:delete",
  DRIVERS_BLACKLIST: "drivers:blacklist",
  VEHICLES_VIEW: "vehicles:view",
  VEHICLES_CREATE: "vehicles:create",
  VEHICLES_EDIT: "vehicles:edit",
  VEHICLES_DELETE: "vehicles:delete",
  CONTRACTS_VIEW: "contracts:view",
  CONTRACTS_CREATE: "contracts:create",
  CONTRACTS_EDIT: "contracts:edit",
  WAYBILLS_VIEW: "waybills:view",
  WAYBILLS_CREATE: "waybills:create",
  WAYBILLS_EDIT: "waybills:edit",
  INSPECTIONS_VIEW: "inspections:view",
  INSPECTIONS_CREATE: "inspections:create",
  INSPECTIONS_EDIT: "inspections:edit",
  MAINTENANCE_VIEW: "maintenance:view",
  MAINTENANCE_CREATE: "maintenance:create",
  MAINTENANCE_EDIT: "maintenance:edit",
  BROADCASTS_VIEW: "broadcasts:view",
  BROADCASTS_CREATE: "broadcasts:create",
  BROADCASTS_SEND: "broadcasts:send",
  GPS_VIEW: "gps:view",
  GPS_EDIT: "gps:edit",
  FINES_VIEW: "fines:view",
  FINES_CREATE: "fines:create",
  FINES_EDIT: "fines:edit",
  REPORTS_VIEW: "reports:view",
  ALVA_VIEW: "alva:view",
  PAYMENT_PROVIDERS_VIEW: "payment-providers:view",
  KASPI_TOPUPS_VIEW: "kaspi-topups:view",
  ONEC_DEBT_VIEW: "onec_debt:view",
  ONEC_DEBT_MANAGE: "onec_debt:manage",
  PNL_VIEW: "pnl:view",
  PNL_EDIT: "pnl:edit",
  PROMOTIONS_VIEW: "promotions:view",
  PROMOTIONS_CREATE: "promotions:create",
  MARKETPLACE_VIEW: "marketplace:view",
  MARKETPLACE_CREATE: "marketplace:create",
  VEHICLES_BLOCK: "vehicles:block",
  VEHICLE_DIRECTORY_VIEW: "vehicle_directory:view",
  VEHICLE_DIRECTORY_CREATE: "vehicle_directory:create",
  VEHICLE_DIRECTORY_EDIT: "vehicle_directory:edit",
  VEHICLE_DIRECTORY_DELETE: "vehicle_directory:delete",
  ORGANIZATIONS_VIEW: "organizations:view",
  ORGANIZATIONS_CREATE: "organizations:create",
  ORGANIZATIONS_EDIT: "organizations:edit",
  ORGANIZATIONS_DELETE: "organizations:delete",
  COUNTERPARTIES_VIEW: "counterparties:view",
  COUNTERPARTIES_CREATE: "counterparties:create",
  COUNTERPARTIES_EDIT: "counterparties:edit",
  COUNTERPARTIES_DELETE: "counterparties:delete",
  DEBTS_VIEW: "debts:view",
  DEBTS_CREATE: "debts:create",
  DEBTS_MANAGE: "debts:manage",
  DEBT_TYPES_VIEW: "debt_types:view",
  DEBT_TYPES_MANAGE: "debt_types:manage",
  FLEET_REASONS_VIEW: "fleet_reasons:view",
  FLEET_REASONS_MANAGE: "fleet_reasons:manage",
  ATTACHMENTS_VIEW: "attachments:view",
  ATTACHMENTS_CREATE: "attachments:create",
  ATTACHMENTS_DELETE: "attachments:delete",
  BALANCE_ADJUST: "balance:adjust",
  YANDEX_WRITEOFF: "yandex:writeoff",
} as const;

export type Permission = (typeof PERMISSIONS)[keyof typeof PERMISSIONS];

// i18n keys for permission-module labels — used by the role permission
// matrix to show a human name instead of the raw `permissions.module` slug.
// Unmapped modules fall back to the raw slug, so drift never drops a row.
export const MODULE_LABELS: Record<string, string> = {
  users: "permModule.users",
  roles: "permModule.roles",
  settings: "permModule.settings",
  audit: "permModule.audit",
  sessions: "permModule.sessions",
  parks: "permModule.parks",
  work_rules: "permModule.workRules",
  cars: "permModule.cars",
  orders: "permModule.orders",
  payouts: "permModule.payouts",
  wallet: "permModule.wallet",
  onec_import: "permModule.onecImport",
  directories: "permModule.directories",
  warehouses: "permModule.warehouses",
  drivers: "permModule.drivers",
  vehicles: "permModule.vehicles",
  contracts: "permModule.contracts",
  waybills: "permModule.waybills",
  inspections: "permModule.inspections",
  maintenance: "permModule.maintenance",
  broadcasts: "permModule.broadcasts",
  gps: "permModule.gps",
  fines: "permModule.fines",
  reports: "permModule.reports",
  alva: "permModule.alva",
  pnl: "permModule.pnl",
  promotions: "permModule.promotions",
  marketplace: "permModule.marketplace",
  vehicle_directory: "permModule.vehicleDirectory",
  organizations: "permModule.organizations",
  counterparties: "permModule.counterparties",
  debts: "permModule.debts",
  debt_types: "permModule.debtTypes",
  fleet_reasons: "permModule.fleetReasons",
  attachments: "permModule.attachments",
  balance: "permModule.balance",
  yandex: "permModule.yandex",
  "payment-providers": "permModule.paymentProviders",
};

// i18n keys for non-standard permission actions (anything outside the
// view/create/edit/delete grid). Rendered as individually labelled
// checkboxes in the matrix "Особые" column so e.g. yandex:writeoff or
// debt_types:manage can be granted without bundling the standard actions.
// Unmapped actions fall back to the raw action string.
export const SPECIAL_ACTION_LABELS: Record<string, string> = {
  writeoff: "permAction.writeoff",
  adjust: "permAction.adjust",
  manage: "permAction.manage",
  block: "permAction.block",
};

// Permission-matrix sections — mirror the navbar grouping
// (components/layout/navbar.tsx) so the role editor and the navbar speak the
// same language. Each section lists its modules in display order; labelKey
// reuses the navbar's own i18n keys. Modules absent from every section land
// in a trailing "other" bucket (permSection.other) — nothing is dropped.
export const PERMISSION_SECTIONS: ReadonlyArray<{
  id: string;
  labelKey: string;
  modules: readonly string[];
}> = [
  { id: "fleet", labelKey: "nav.fleet", modules: ["vehicles", "cars", "drivers", "parks", "organizations", "work_rules", "vehicle_directory"] },
  { id: "docs", labelKey: "nav.docs", modules: ["contracts", "counterparties", "waybills", "fines"] },
  { id: "operations", labelKey: "nav.operations", modules: ["inspections", "maintenance", "warehouses", "alva"] },
  { id: "monitoring", labelKey: "nav.monitoring", modules: ["gps"] },
  { id: "finance", labelKey: "nav.finance", modules: ["pnl", "reports", "debts", "balance", "yandex", "wallet", "payouts", "orders", "payment-providers"] },
  { id: "communications", labelKey: "nav.communications", modules: ["broadcasts", "marketplace", "promotions"] },
  { id: "management", labelKey: "nav.management", modules: ["settings", "directories", "debt_types", "fleet_reasons", "onec_import", "users", "roles"] },
  { id: "system", labelKey: "permSection.system", modules: ["audit", "sessions", "attachments"] },
];

// Permissions that only superadmin can delegate — mirror
// admin-panel/backend/internal/constants/escalation.go EscalationPermissions.
// Used by /users/new to hide roles a park-admin can't actually assign
// (the backend would reject with 403). The handler is the source of
// truth; this list is a UI hint to avoid the user seeing an option they
// can't pick. Keep in sync manually — drift will surface as a 403 toast
// rather than data loss.
export const ESCALATION_PERMISSION_SLUGS: readonly Permission[] = [
  PERMISSIONS.USERS_CREATE,
  PERMISSIONS.USERS_EDIT,
  PERMISSIONS.USERS_DELETE,
  PERMISSIONS.ROLES_CREATE,
  PERMISSIONS.ROLES_EDIT,
  PERMISSIONS.ROLES_DELETE,
  PERMISSIONS.PARKS_CREATE,
  PERMISSIONS.PARKS_DELETE,
  PERMISSIONS.AUDIT_VIEW,
  PERMISSIONS.ORGANIZATIONS_CREATE,
  PERMISSIONS.ORGANIZATIONS_DELETE,
] as const;

// Role slugs — mirror admin-panel/backend/internal/constants/roles.go.
// Backend seeds them in migration 000001_schema.up.sql:2652-2659.
export const ROLE_SLUGS = {
  SUPERADMIN: "superadmin",
  PARK_ADMIN: "park-admin",
  MANAGER: "manager",
  MECHANIC: "mechanic",
  STOREKEEPER: "storekeeper",
  DISPATCHER: "dispatcher",
  READONLY: "readonly",
  SERVICE_CENTER: "service_center",
} as const;

export type RoleSlug = (typeof ROLE_SLUGS)[keyof typeof ROLE_SLUGS];

// Shared CSS class for native select inputs
export const SELECT_CLASS =
  "h-10 w-full rounded-lg border border-[var(--input)] bg-[var(--background)] px-3 text-sm text-[var(--foreground)]";

// Compact native select/input for toolbar filters (h-9, auto-width).
export const FILTER_CONTROL =
  "h-9 rounded-md border border-[var(--input)] bg-[var(--background)] px-2.5 text-sm text-[var(--foreground)] focus:outline-none focus:ring-2 focus:ring-[var(--ring)]";

// Sticky table header for bounded-height scroll containers. Each <th> needs its
// own opaque background so body rows pass underneath it while scrolling.
// The wrapping scroll container should also carry `isolate` (isolation: isolate)
// so this z-10 stays contained — otherwise it competes with toolbar dropdowns
// at the same z-index and the sticky cells paint over them.
export const STICKY_TABLE_HEAD = "sticky top-0 z-10 bg-[var(--card)]";

// Fallback width (px) for a virtualized list column with no entry in its
// page's COL_WIDTHS map. Tables use table-fixed so widths come from the header.
export const DEFAULT_COL_WIDTH = 150;

// Highlight for rows / controls scoped to the current user ("Мои водители",
// "Моя смена" и т.п.). Keeps the amber palette in one place.
export const HIGHLIGHT_MINE_ROW =
  "bg-amber-50 hover:bg-amber-100 dark:bg-amber-950/20 dark:hover:bg-amber-950/30";
export const HIGHLIGHT_MINE_ACTIVE =
  "bg-amber-100 text-amber-900 hover:bg-amber-200 dark:bg-amber-900/40 dark:text-amber-100";

// Driver statuses (Yandex Fleet aligned)
export const DRIVER_STATUSES = {
  WORKING: "working",
  FIRED: "fired",
  SUSPENDED: "suspended",
  NOT_WORKING: "not_working",
  BLOCKED: "blocked",
} as const;

// i18n keys for driver status labels — use with t()
export const DRIVER_STATUS_LABELS: Record<string, string> = {
  [DRIVER_STATUSES.WORKING]: "driverStatus.working",
  [DRIVER_STATUSES.FIRED]: "driverStatus.fired",
  [DRIVER_STATUSES.SUSPENDED]: "driverStatus.suspended",
  [DRIVER_STATUSES.NOT_WORKING]: "driverStatus.notWorking",
  [DRIVER_STATUSES.BLOCKED]: "driverStatus.blocked",
};

// Shared <textarea> styling (mirrors SELECT_CLASS) for reason / comment inputs.
export const TEXTAREA_CLASS =
  "w-full rounded-md border border-[var(--input)] bg-[var(--background)] px-3 py-2 text-sm shadow-sm placeholder:text-[var(--muted-foreground)] focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-[var(--ring)] dark:bg-[var(--background)] dark:border-[var(--border)]";

export const DRIVER_STATUS_OPTIONS = [
  { value: DRIVER_STATUSES.WORKING, labelKey: DRIVER_STATUS_LABELS[DRIVER_STATUSES.WORKING] },
  { value: DRIVER_STATUSES.NOT_WORKING, labelKey: DRIVER_STATUS_LABELS[DRIVER_STATUSES.NOT_WORKING] },
  { value: DRIVER_STATUSES.SUSPENDED, labelKey: DRIVER_STATUS_LABELS[DRIVER_STATUSES.SUSPENDED] },
  { value: DRIVER_STATUSES.BLOCKED, labelKey: DRIVER_STATUS_LABELS[DRIVER_STATUSES.BLOCKED] },
  { value: DRIVER_STATUSES.FIRED, labelKey: DRIVER_STATUS_LABELS[DRIVER_STATUSES.FIRED] },
] as const;

// Vehicle status i18n keys — use with t()
//
// Two layers coexist on purpose:
//   * Editable product slugs (free / rented / service / accident / sold) drive
//     the inline-edit popover on the vehicles list and the PATCH endpoint.
//   * Legacy / Yandex-mirror slugs (working / no_driver / not_working /
//     repairing / unknown / pending) still show up on read-only paths for
//     historical rows; we keep their labels so badges don't render the raw
//     slug. The popover only offers the editable five.
export const VEHICLE_STATUS_LABELS: Record<string, string> = {
  working: "vehicleStatus.working",
  no_driver: "vehicleStatus.noDriver",
  not_working: "vehicleStatus.notWorking",
  rented: "vehicleStatus.rented",
  repairing: "vehicleStatus.repairing",
  unknown: "vehicleStatus.unknown",
  pending: "vehicleStatus.pending",
  insurance: "vehicleStatus.insurance",
  impound: "vehicleStatus.impound",
  accident: "vehicleStatus.accident",
  // Editable product slugs (PATCH /cars/:id/status)
  free: "vehicleStatus.free",
  service: "vehicleStatus.service",
  sold: "vehicleStatus.sold",
  // Contract-driven (mig 000308): set and cleared by a buyout rental contract,
  // never by the operator — the badge is re-derived from the in-force contract
  // server-side, so hand-setting it on a plain rental just reads «В аренде».
  buyout: "vehicleStatus.buyout",
  // legacy aliases — keep for rows imported via older Yandex sync
  maintenance: "vehicleStatus.maintenance",
  repair: "vehicleStatus.repairing",
};

// Editable vehicle status slugs — exactly the five values the backend's
// PATCH /cars/:id/status endpoint accepts (validator: oneof=free rented
// service accident sold). Existing rows may still carry legacy slugs from
// Yandex sync; those render via VEHICLE_STATUS_LABELS but the popover
// won't surface them as choices.
export const VEHICLE_STATUSES = {
  FREE: "free",
  RENTED: "rented",
  SERVICE: "service",
  ACCIDENT: "accident",
  SOLD: "sold",
} as const;

export type VehicleStatusEditable =
  (typeof VEHICLE_STATUSES)[keyof typeof VEHICLE_STATUSES];

export const VEHICLE_STATUS_EDITABLE_OPTIONS = [
  { value: VEHICLE_STATUSES.FREE, labelKey: "vehicleStatus.free" },
  { value: VEHICLE_STATUSES.RENTED, labelKey: "vehicleStatus.rented" },
  { value: VEHICLE_STATUSES.SERVICE, labelKey: "vehicleStatus.service" },
  { value: VEHICLE_STATUSES.ACCIDENT, labelKey: "vehicleStatus.accident" },
  { value: VEHICLE_STATUSES.SOLD, labelKey: "vehicleStatus.sold" },
] as const;

// Color palette for vehicle status badges. Includes both editable slugs and
// legacy aliases so any status the backend may return renders consistently.
// Always paired with a dark: variant — required by the design system.
export const VEHICLE_STATUS_COLORS: Record<string, string> = {
  free: "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-400",
  rented: "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400",
  service: "bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-400",
  accident: "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400",
  sold: "bg-gray-100 text-gray-800 dark:bg-gray-900/30 dark:text-gray-400",
  // Violet, deliberately far from rental blue — «Выкуп» vs «В аренде» is the
  // one pair an operator must never confuse at a glance.
  buyout: "bg-violet-100 text-violet-800 dark:bg-violet-900/30 dark:text-violet-400",
  // Legacy / Yandex-mirror slugs — fall back to a sensible color so old rows
  // don't render with the gray "unknown" pill.
  working: "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-400",
  no_driver: "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-400",
  not_working: "bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-400",
  repairing: "bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-400",
  maintenance: "bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-400",
  repair: "bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-400",
  unknown: "bg-gray-100 text-gray-800 dark:bg-gray-900/30 dark:text-gray-400",
  pending: "bg-gray-100 text-gray-800 dark:bg-gray-900/30 dark:text-gray-400",
};

// OSAGO insurance expiry indicator. `osago_status` is computed server-side per
// the vehicle's park threshold; the client only maps the enum to an icon
// colour + label. Only "expired"/"expiring" carry an indicator — "ok"/"none"/
// "off" render nothing. Always paired with a dark: variant (design system).
export const OSAGO_STATUS = {
  EXPIRED: "expired",
  EXPIRING: "expiring",
  OK: "ok",
  NONE: "none",
  OFF: "off",
} as const;

export const OSAGO_STATUS_META: Record<string, { color: string; labelKey: string }> = {
  [OSAGO_STATUS.EXPIRED]: { color: "text-red-600 dark:text-red-400", labelKey: "osago.status.expired" },
  [OSAGO_STATUS.EXPIRING]: { color: "text-amber-600 dark:text-amber-400", labelKey: "osago.status.expiring" },
};

// Гостехосмотр (государственный технический осмотр) expiry indicator — mirrors
// OSAGO_STATUS exactly. `tech_inspection_status` is computed server-side per the
// vehicle's park threshold; the client only maps the enum to an icon colour +
// label. Only "expired"/"expiring" carry an indicator — "ok"/"none"/"off"
// render nothing. Always paired with a dark: variant (design system).
export const TECH_INSPECTION_STATUS = {
  EXPIRED: "expired",
  EXPIRING: "expiring",
  OK: "ok",
  NONE: "none",
  OFF: "off",
} as const;

export const TECH_INSPECTION_STATUS_META: Record<string, { color: string; labelKey: string }> = {
  [TECH_INSPECTION_STATUS.EXPIRED]: { color: "text-red-600 dark:text-red-400", labelKey: "techInspection.status.expired" },
  [TECH_INSPECTION_STATUS.EXPIRING]: { color: "text-amber-600 dark:text-amber-400", labelKey: "techInspection.status.expiring" },
};

// Maintenance (ТО) expiry indicator — mirrors OSAGO_STATUS. `to_status` is
// computed server-side from the vehicle's maintenance schedules cache; the
// client only maps the enum to an icon colour + label. Only "expired"/
// "expiring" carry an indicator — "ok"/"none" render nothing. No "off" value:
// ТО has no per-park threshold to disable. Always paired with a dark: variant.
export const TO_STATUS = {
  EXPIRED: "expired",
  EXPIRING: "expiring",
  OK: "ok",
  NONE: "none",
} as const;

export const TO_STATUS_META: Record<string, { color: string; labelKey: string }> = {
  [TO_STATUS.EXPIRED]: { color: "text-red-600 dark:text-red-400", labelKey: "to.status.expired" },
  [TO_STATUS.EXPIRING]: { color: "text-amber-600 dark:text-amber-400", labelKey: "to.status.expiring" },
};

// Waybill (путевой лист) expiry indicator — mirrors OSAGO_STATUS exactly.
// `waybill_status` is computed server-side from the driver's CURRENT open
// waybill valid_to vs the driver's park waybill_warn_days. Only "expired"/
// "expiring" carry an indicator — "ok"/"none"/"off" render nothing. Always
// paired with a dark: variant (design system).
export const WAYBILL_EXPIRY_STATUS = {
  EXPIRED: "expired",
  EXPIRING: "expiring",
  OK: "ok",
  NONE: "none",
  OFF: "off",
} as const;

export const WAYBILL_EXPIRY_STATUS_META: Record<string, { color: string; labelKey: string }> = {
  [WAYBILL_EXPIRY_STATUS.EXPIRED]: { color: "text-red-600 dark:text-red-400", labelKey: "waybillExpiry.status.expired" },
  [WAYBILL_EXPIRY_STATUS.EXPIRING]: { color: "text-amber-600 dark:text-amber-400", labelKey: "waybillExpiry.status.expiring" },
};

// Rental-contract (договор аренды) expiry indicator — mirrors WAYBILL_EXPIRY_STATUS
// exactly. `contract_status` is computed server-side from the driver's CURRENT
// active contract end_date vs the park contract_warn_days; on the contracts list
// `expiry_status` is the same value per row. Only "expired"/"expiring" carry an
// indicator — "ok"/"none"/"off" render nothing.
export const CONTRACT_EXPIRY_STATUS = {
  EXPIRED: "expired",
  EXPIRING: "expiring",
  OK: "ok",
  NONE: "none",
  OFF: "off",
} as const;

export const CONTRACT_EXPIRY_STATUS_META: Record<string, { color: string; labelKey: string }> = {
  [CONTRACT_EXPIRY_STATUS.EXPIRED]: { color: "text-red-600 dark:text-red-400", labelKey: "contractExpiry.status.expired" },
  [CONTRACT_EXPIRY_STATUS.EXPIRING]: { color: "text-amber-600 dark:text-amber-400", labelKey: "contractExpiry.status.expiring" },
};

// Осмотр приёма («принял ли водитель машину сам, в приложении»).
// `intake_status` считается на сервере по ДЕЙСТВУЮЩЕМУ договору — тому же, что
// дал номер машины рядом (repository/driver_doc_expr.go). Пустая строка =
// действующего договора нет: машины у человека нет, и значка тоже.
//
// Тут, в отличие от соседей выше, рисуются ВСЕ три значения, включая нейтральное:
// смысл значка — ответить на вопрос, а не подсветить аварию, поэтому «принял»
// обязано быть видно так же явно, как «не принял».
export const INTAKE_STATUS = {
  ACCEPTED: "accepted",
  NOT_ACCEPTED: "not_accepted",
  // Строки осмотра нет вовсе — так выглядят договоры, залитые переносами 1С
  // мимо ContractService.Create. Баннера «Примите автомобиль» у такого водителя
  // нет, закрыть ему нечего — красный был бы обвинением на пустом месте.
  NO_RECORD: "no_record",
} as const;

export const INTAKE_STATUS_META: Record<
  string,
  { fill: string; glyph: "check" | "cross" | "dash"; labelKey: string }
> = {
  [INTAKE_STATUS.ACCEPTED]: {
    fill: "text-emerald-500 dark:text-emerald-400",
    glyph: "check",
    labelKey: "intake.status.accepted",
  },
  [INTAKE_STATUS.NOT_ACCEPTED]: {
    fill: "text-red-500 dark:text-red-400",
    glyph: "cross",
    labelKey: "intake.status.notAccepted",
  },
  [INTAKE_STATUS.NO_RECORD]: {
    fill: "text-zinc-400 dark:text-zinc-500",
    glyph: "dash",
    labelKey: "intake.status.noRecord",
  },
};

// Status badge colors
export const DRIVER_STATUS_COLORS: Record<string, string> = {
  [DRIVER_STATUSES.WORKING]: "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-400",
  [DRIVER_STATUSES.SUSPENDED]: "bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-400",
  [DRIVER_STATUSES.NOT_WORKING]: "bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-400",
  [DRIVER_STATUSES.FIRED]: "bg-gray-100 text-gray-800 dark:bg-gray-900/30 dark:text-gray-400",
  [DRIVER_STATUSES.BLOCKED]: "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400",
};

// Status → Badge variant mapping
export function driverStatusVariant(status: string) {
  switch (status) {
    case DRIVER_STATUSES.WORKING:
      return "success" as const;
    case DRIVER_STATUSES.FIRED:
    case DRIVER_STATUSES.BLOCKED:
      return "secondary" as const;
    case DRIVER_STATUSES.SUSPENDED:
    case DRIVER_STATUSES.NOT_WORKING:
      return "destructive" as const;
    default:
      return "outline" as const;
  }
}

// Fleet employment types (Yandex Fleet API)
export const FLEET_EMPLOYMENT_TYPES = {
  PARK_EMPLOYEE: "park_employee",
  SELFEMPLOYED: "selfemployed",
} as const;

// driver_sources.kind values — mig 000068 CHECK constraint.
export const DRIVER_SOURCE_KINDS = {
  GENERAL: "general",
  REFERRAL: "driver_referral",
} as const;

// park_drivers.dismissal_reason values written by automated flows. Operator-set
// reasons are arbitrary text; only the system-emitted ones get UI affordances.
export const DISMISSAL_REASONS = {
  TAX_TYPE_SWITCHED: "tax_type_switched",
  // Mirrors backend constants.MsgTransferredToOtherPark — written verbatim into
  // park_drivers.dismissal_reason when a driver is moved to another park, so it
  // surfaces (in English) on cross-park account lists unless humanized.
  TRANSFERRED_TO_PARK: "transferred to another park",
} as const;

export const FLEET_EMPLOYMENT_OPTIONS = [
  { value: FLEET_EMPLOYMENT_TYPES.PARK_EMPLOYEE, labelKey: "fleet.employment.parkEmployee" },
  { value: FLEET_EMPLOYMENT_TYPES.SELFEMPLOYED, labelKey: "fleet.employment.selfemployed" },
] as const;

// Самозанятый predicate for an employment_type value as stored in
// park_drivers. Synced rows hold individual_entrepreneur (Yandex's read-side
// spelling for EVERY СМЗ — prod 2026-06-12: all 35k+) or legacy
// self_employed*; never compare against "selfemployed" literally. Mirrors the
// backend's constants.IsSelfemployedType.
export function isSelfemployedType(empType?: string | null): boolean {
  return !!empType && empType !== FLEET_EMPLOYMENT_TYPES.PARK_EMPLOYEE;
}

// Display label key for an employment_type value as stored in park_drivers.
// Tolerant of all sync spellings via isSelfemployedType, so a stray spelling
// never renders the wrong kind.
export function employmentTypeLabelKey(empType?: string | null): string {
  return isSelfemployedType(empType)
    ? "fleet.employment.selfemployed"
    : "fleet.employment.parkEmployee";
}

// Fleet professions (Yandex Fleet API)
export const FLEET_PROFESSIONS = {
  TAXI_DRIVER: "taxi/driver",
  COURIER_CAR: "cargo/courier/on-car",
  COURIER_TRUCK: "cargo/courier/on-truck",
} as const;

export const FLEET_PROFESSION_OPTIONS = [
  { value: FLEET_PROFESSIONS.TAXI_DRIVER, labelKey: "fleet.profession.taxiDriver" },
  { value: FLEET_PROFESSIONS.COURIER_CAR, labelKey: "fleet.profession.courierCar" },
  { value: FLEET_PROFESSIONS.COURIER_TRUCK, labelKey: "fleet.profession.courierTruck" },
] as const;

// Rental contract types — matches backend dto/request/contract.go validator.
export const CONTRACT_TYPES = {
  WITH_DRIVER: "with_driver",
  WITH_CONTRACTOR: "with_contractor",
  BUYOUT: "buyout",
} as const;

export type ContractType = (typeof CONTRACT_TYPES)[keyof typeof CONTRACT_TYPES];

// The balances an internal operation may address. Mirrors
// constants.InternalTargetBalances on the backend — the drift test in
// constants/drift_test.go fails if the two lists diverge.
//
// This is the WRITE-set, not the full set of values driver_transactions can
// hold. Historical rows also carry "balance" (the Yandex.Pro wallet mirror,
// which the wallet sync overwrites every tick — operations against it were
// phantoms and are no longer accepted; real wallet movement goes through the
// «Яндекс» mode) and "deposit". Both still render in history and in the
// manual-adjustments register; neither may be written again.
export const TARGET_BALANCES = {
  RENTAL: "rental",
  BUYOUT: "buyout",
} as const;

export type TargetBalance = (typeof TARGET_BALANCES)[keyof typeof TARGET_BALANCES];

// The transaction types allowed per target. The split between a debit type and
// a credit type is load-bearing on the backend (constants.IsDebitTxType flips
// the sign of a debit), so each target gets its own fee/payment/correction
// triple rather than sharing a generic "payment".
export const TX_TYPES_BY_TARGET: Record<TargetBalance, readonly string[]> = {
  [TARGET_BALANCES.RENTAL]: ["rental_fee", "rental_payment", "rental_correction"],
  [TARGET_BALANCES.BUYOUT]: ["buyout_fee", "buyout_payment", "buyout_correction"],
};

// Tuple form for Zod's z.enum() — keep in sync with CONTRACT_TYPES.
export const CONTRACT_TYPE_VALUES = [
  CONTRACT_TYPES.WITH_DRIVER,
  CONTRACT_TYPES.WITH_CONTRACTOR,
  CONTRACT_TYPES.BUYOUT,
] as const;

export const CONTRACT_TYPE_LABELS: Record<string, string> = {
  [CONTRACT_TYPES.WITH_DRIVER]: "contractType.withDriver",
  [CONTRACT_TYPES.WITH_CONTRACTOR]: "contractType.withContractor",
  [CONTRACT_TYPES.BUYOUT]: "contractType.buyout",
};

// Counterparty (арендатор) types — matches DB check (mig 000174). Default is
// 'legal' (the DB column default), so an unknown value reads as legal.
export const COUNTERPARTY_TYPE_LABELS: Record<string, string> = {
  legal: "counterparties.typeLegal",
  individual: "counterparties.typeIndividual",
};

// Rental contract statuses — matches backend constants/statuses.go.
export const CONTRACT_STATUS_LABELS: Record<string, string> = {
  active: "contractStatus.active",
  terminated: "contractStatus.terminated",
  expired: "contractStatus.expired",
};

export function contractStatusVariant(status: string) {
  switch (status) {
    case "active": return "success" as const;
    case "terminated": return "destructive" as const;
    case "expired": return "secondary" as const;
    default: return "outline" as const;
  }
}

export const WAYBILL_STATUS_LABELS: Record<string, string> = {
  draft: "waybillStatus.draft",
  issued: "waybillStatus.issued",
  returned: "waybillStatus.returned",
  cancelled: "waybillStatus.cancelled",
  active: "waybillStatus.active",
  superseded: "waybillStatus.superseded",
};

export const LOAN_STATUS_LABELS: Record<string, string> = {
  active: "loanStatus.active",
  paid_off: "loanStatus.paidOff",
  overdue: "loanStatus.overdue",
  cancelled: "loanStatus.cancelled",
  pending: "loanStatus.pending",
  paid: "loanStatus.paid",
};

// Inspection slot labels moved to lib/attachments.ts — INSPECTION_SLOT_LABEL_KEYS.
// Photos themselves now live in the attachments domain (S3).

export const INSPECTION_TYPE_LABELS: Record<string, string> = {
  intake: "inspectionType.intake",
  release: "inspectionType.release",
  planned: "inspectionType.planned",
  manual: "inspectionType.manual",
};

export const INSPECTION_ROLE_LABELS: Record<string, string> = {
  driver: "inspectionRole.driver",
  mechanic: "inspectionRole.mechanic",
  admin: "inspectionRole.admin",
};

// Order matters: it drives the status filter menu in the осмотры journal
// (lib/inspection-filters.ts reads this map as its option list). Append new
// values, don't reorder.
export const INSPECTION_STATUS_LABELS: Record<string, string> = {
  completed: "inspectionStatus.completed",
  pending: "inspectionStatus.pending",
  // Terminal: the rental ended before the driver accepted the car, so
  // ContractRepository.Terminate cancelled the pending intake (mig 000238).
  cancelled: "inspectionStatus.cancelled",
  // Terminal: a planned осмотр nobody completed within its schedule's cadence.
  // The inspection cron expires it and issues a fresh one — without that the
  // stale row blocked its vehicle from ever being scheduled again.
  superseded: "inspectionStatus.superseded",
};

export const DOCUMENT_STATUS_MAP: Record<string, { labelKey: string; variant: "warning" | "success" }> = {
  draft: { labelKey: "documentStatus.draft", variant: "warning" },
  posted: { labelKey: "documentStatus.posted", variant: "success" },
};

// Debt statuses
export const DEBT_STATUSES = {
  ACTIVE: "active",
  PAID: "paid",
  CANCELLED: "cancelled",
} as const;

export const DEBT_STATUS_LABELS: Record<string, string> = {
  [DEBT_STATUSES.ACTIVE]: "debtStatus.active",
  [DEBT_STATUSES.PAID]: "debtStatus.paid",
  [DEBT_STATUSES.CANCELLED]: "debtStatus.cancelled",
};

export const DEBT_STATUS_COLORS: Record<string, string> = {
  [DEBT_STATUSES.ACTIVE]: "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400",
  [DEBT_STATUSES.PAID]: "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-400",
  [DEBT_STATUSES.CANCELLED]: "bg-gray-100 text-gray-800 dark:bg-gray-900/30 dark:text-gray-400",
};

export const DEBT_OPERATION_TYPES = {
  CHARGE: "charge",
  PAYMENT: "payment",
} as const;

export const DEBT_OPERATION_LABELS: Record<string, string> = {
  [DEBT_OPERATION_TYPES.CHARGE]: "debtOperation.charge",
  [DEBT_OPERATION_TYPES.PAYMENT]: "debtOperation.payment",
};

// Fines
export const FINE_STATUSES = {
  UNPAID: "unpaid",
  PAID: "paid",
  OVERDUE: "overdue",
  DISPUTED: "disputed",
} as const;

// Mig 000066. When a payment_method.aggregator_kind matches one of these,
// fine payments via that method trigger an automatic wallet writeoff via
// the matching aggregator service (Phase B). Add new kinds (bolt/indriver)
// here, in the DB CHECK, in i18n labels, and in the constants/aggregator.go
// backend mirror.
export const AGGREGATOR_KINDS = {
  YANDEX_FLEET: "yandex_fleet",
} as const;

export type AggregatorKind = (typeof AGGREGATOR_KINDS)[keyof typeof AGGREGATOR_KINDS];

export const AGGREGATOR_KIND_OPTIONS: AggregatorKind[] = [AGGREGATOR_KINDS.YANDEX_FLEET];

// Mig 000067 — fine_charge_intent.status. Mirrors the DB CHECK; used by
// the pay-modal's success handler to dispatch the right toast.
export const FINE_CHARGE_INTENT_STATUSES = {
  OPEN: "open",
  PARTIAL: "partial",
  PAID: "paid",
  CANCELLED: "cancelled",
} as const;

export type FineChargeIntentStatus =
  (typeof FINE_CHARGE_INTENT_STATUSES)[keyof typeof FINE_CHARGE_INTENT_STATUSES];

export function fineChargeIntentStatusVariant(status: string) {
  switch (status) {
    case FINE_CHARGE_INTENT_STATUSES.PAID:
      return "success" as const;
    case FINE_CHARGE_INTENT_STATUSES.PARTIAL:
      return "warning" as const;
    case FINE_CHARGE_INTENT_STATUSES.CANCELLED:
      return "secondary" as const;
    default:
      return "outline" as const;
  }
}

export type FineStatus = (typeof FINE_STATUSES)[keyof typeof FINE_STATUSES];

export const FINE_STATUS_LABELS: Record<string, string> = {
  [FINE_STATUSES.UNPAID]: "fineStatus.unpaid",
  [FINE_STATUSES.PAID]: "fineStatus.paid",
  [FINE_STATUSES.OVERDUE]: "fineStatus.overdue",
  [FINE_STATUSES.DISPUTED]: "fineStatus.disputed",
};

export function fineStatusVariant(status: string) {
  switch (status) {
    case FINE_STATUSES.PAID:
      return "success" as const;
    case FINE_STATUSES.UNPAID:
    case FINE_STATUSES.OVERDUE:
      return "destructive" as const;
    case FINE_STATUSES.DISPUTED:
      return "warning" as const;
    default:
      return "outline" as const;
  }
}

export const FINE_SOURCES = {
  MANUAL: "manual",
  EGOV: "egov",
  IMPORT: "import",
} as const;

export type FineSource = (typeof FINE_SOURCES)[keyof typeof FINE_SOURCES];

export const FINE_SOURCE_LABELS: Record<string, string> = {
  [FINE_SOURCES.MANUAL]: "fines.sourceManual",
  [FINE_SOURCES.EGOV]: "fines.sourceEgov",
  [FINE_SOURCES.IMPORT]: "fines.sourceImport",
};

// Fine `paid_by` — backend stores the raw string (dto/request/fine.go:46 has no
// `oneof` validator). Existing prod rows persist Cyrillic values, so changing
// these to English would silently desync filters/reports. Display via i18n.
export const FINE_PAID_BY = {
  DRIVER: "Водитель",
  COMPANY: "Компания",
} as const;

export type FinePaidBy = (typeof FINE_PAID_BY)[keyof typeof FINE_PAID_BY];

export const FINE_PAID_BY_LABELS: Record<string, string> = {
  [FINE_PAID_BY.DRIVER]: "finePaidBy.driver",
  [FINE_PAID_BY.COMPANY]: "finePaidBy.company",
};

// Driver gender
export const GENDER = {
  MALE: "male",
  FEMALE: "female",
} as const;

export type Gender = (typeof GENDER)[keyof typeof GENDER];

// Fuel / transmission codes — backend authoritative source is
// /directories/fuel-types and /directories/transmission-types (seeded in
// migration 000001). These maps localize the seeded codes; admin-added types
// fall back to the directory's RU name. Vehicle storage column is `fuel_type`
// (string, no oneof). Code = directory.code = `vehicles.fuel_type`.
export const FUEL_TYPE_LABEL_KEYS: Record<string, string> = {
  petrol: "fleet.fuel.petrol",
  diesel: "fleet.fuel.diesel",
  gas: "fleet.fuel.gas",
  electric: "fleet.fuel.electric",
  hybrid: "fleet.fuel.hybrid",
};

export const TRANSMISSION_LABEL_KEYS: Record<string, string> = {
  automatic: "vehicleNew.automatic",
  manual: "vehicleNew.manual",
  cvt: "vehicleNew.variator",
  robot: "vehicleNew.robot",
};

// ─── Maintenance schedules (ТО) — mirrors backend constants/statuses.go ───
// Drift guarded by backend constants/drift_test.go. Keep values identical.

// A schedule fires either on mileage/engine-hours OR on a calendar recurrence.
export const MAINTENANCE_TRIGGER_TYPES = {
  MILEAGE: "mileage",
  CALENDAR: "calendar",
} as const;

export type MaintenanceTriggerType =
  (typeof MAINTENANCE_TRIGGER_TYPES)[keyof typeof MAINTENANCE_TRIGGER_TYPES];

// Calendar recurrence patterns (when trigger = calendar).
export const MAINTENANCE_CALENDAR_PATTERNS = {
  WEEKLY: "weekly",
  EVERY_N_WEEKS: "every_n_weeks",
  MONTHLY_NTH_WEEKDAY: "monthly_nth_weekday",
  EVERY_N_MONTHS: "every_n_months",
} as const;

export type MaintenanceCalendarPattern =
  (typeof MAINTENANCE_CALENDAR_PATTERNS)[keyof typeof MAINTENANCE_CALENDAR_PATTERNS];

// week_of_month sentinel for "last weekday of the month".
export const MAINTENANCE_WEEK_OF_MONTH_LAST = -1;

// Assignment targets. Priority on overlap: batch > model > park.
export const MAINTENANCE_TARGET_TYPES = {
  PARK: "park",
  MODEL: "model",
  BATCH: "batch",
} as const;

export type MaintenanceTargetType =
  (typeof MAINTENANCE_TARGET_TYPES)[keyof typeof MAINTENANCE_TARGET_TYPES];

export const MAINTENANCE_TRIGGER_LABELS: Record<string, string> = {
  [MAINTENANCE_TRIGGER_TYPES.MILEAGE]: "maintenanceSchedules.triggerMileage",
  [MAINTENANCE_TRIGGER_TYPES.CALENDAR]: "maintenanceSchedules.triggerCalendar",
};

export const MAINTENANCE_CALENDAR_PATTERN_LABELS: Record<string, string> = {
  [MAINTENANCE_CALENDAR_PATTERNS.WEEKLY]: "maintenanceSchedules.patternWeekly",
  [MAINTENANCE_CALENDAR_PATTERNS.EVERY_N_WEEKS]: "maintenanceSchedules.patternEveryNWeeks",
  [MAINTENANCE_CALENDAR_PATTERNS.MONTHLY_NTH_WEEKDAY]: "maintenanceSchedules.patternMonthlyNthWeekday",
  [MAINTENANCE_CALENDAR_PATTERNS.EVERY_N_MONTHS]: "maintenanceSchedules.patternEveryNMonths",
};

export const MAINTENANCE_TARGET_LABELS: Record<string, string> = {
  [MAINTENANCE_TARGET_TYPES.PARK]: "maintenanceSchedules.targetPark",
  [MAINTENANCE_TARGET_TYPES.MODEL]: "maintenanceSchedules.targetModel",
  [MAINTENANCE_TARGET_TYPES.BATCH]: "maintenanceSchedules.targetBatch",
};

// Control-list warning states (mirrors vehicle_maintenance_status.warning_state).
export const MAINTENANCE_WARNING_LABELS: Record<string, string> = {
  approaching: "maintenanceControl.approaching",
  warning: "maintenanceControl.warning",
  overdue: "maintenanceControl.overdue",
};

export const maintenanceWarningVariant = (state: string) => {
  switch (state) {
    case "overdue": return "destructive" as const;
    case "warning": return "default" as const;
    case "approaching": return "secondary" as const;
    default: return "outline" as const;
  }
};

// Sort rank for warning states (most urgent first) — mirrors the backend's
// control-list ordering, used by the client-side column sort.
export const maintenanceWarningRank = (state: string): number => {
  switch (state) {
    case "overdue": return 0;
    case "warning": return 1;
    case "approaching": return 2;
    default: return 3;
  }
};

// Urgency palette for warning_state — kept here (next to the variant/rank
// helpers) so the Контроль due-cell text and row accent can't drift from the
// red/amber the vehicles-list ТО banner uses.
export const maintenanceWarningTone = (state: string): string => {
  if (state === "overdue") return "text-red-600 dark:text-red-400";
  if (state === "warning" || state === "approaching") return "text-amber-600 dark:text-amber-400";
  return "text-[var(--foreground)]";
};

export const maintenanceWarningAccent = (state: string): string => {
  if (state === "overdue") return "border-l-2 border-red-500 dark:border-red-400";
  if (state === "warning" || state === "approaching") return "border-l-2 border-amber-500 dark:border-amber-400";
  return "";
};

// ─── Vehicle downtime register (СТО / ДТП / страховая / штрафстоянка, mig 000216) ───
// Severity reuses maintenanceWarningTone / maintenanceWarningAccent above — the
// downtime `severity` field shares the warning_state vocabulary, so no new
// colour map is invented for it.
export const DOWNTIME_CASE_KINDS = {
  REPAIR: "repair",
  ACCIDENT: "accident",
  INSURANCE: "insurance",
  IMPOUND: "impound",
} as const;

export type DowntimeCaseKind =
  (typeof DOWNTIME_CASE_KINDS)[keyof typeof DOWNTIME_CASE_KINDS];

// Form/select order — СТО first (most common), then the legal/insurance trio.
export const DOWNTIME_CASE_KIND_ORDER: DowntimeCaseKind[] = [
  DOWNTIME_CASE_KINDS.REPAIR,
  DOWNTIME_CASE_KINDS.ACCIDENT,
  DOWNTIME_CASE_KINDS.INSURANCE,
  DOWNTIME_CASE_KINDS.IMPOUND,
];

// The "Страховая / Штрафстоянка" register block groups every non-repair kind.
export const DOWNTIME_LEGAL_KINDS: DowntimeCaseKind[] = [
  DOWNTIME_CASE_KINDS.ACCIDENT,
  DOWNTIME_CASE_KINDS.INSURANCE,
  DOWNTIME_CASE_KINDS.IMPOUND,
];

// Kinds that expose the free-text «Локация» field: every non-repair (legal)
// case has a physical place (ГАИ / штрафстоянка / страховая) but no service
// centre. Repair names its place via service_center / «на базе» instead.
export const DOWNTIME_LOCATION_KINDS: DowntimeCaseKind[] = [
  DOWNTIME_CASE_KINDS.ACCIDENT,
  DOWNTIME_CASE_KINDS.INSURANCE,
  DOWNTIME_CASE_KINDS.IMPOUND,
];

// i18n keys for case-kind labels — use with t()
export const DOWNTIME_CASE_KIND_LABELS: Record<string, string> = {
  [DOWNTIME_CASE_KINDS.REPAIR]: "downtime.caseKind.repair",
  [DOWNTIME_CASE_KINDS.ACCIDENT]: "downtime.caseKind.accident",
  [DOWNTIME_CASE_KINDS.INSURANCE]: "downtime.caseKind.insurance",
  [DOWNTIME_CASE_KINDS.IMPOUND]: "downtime.caseKind.impound",
};

// Badge tint per kind (matches the dashboard movement cards' palette).
export const DOWNTIME_CASE_KIND_BADGE: Record<string, string> = {
  [DOWNTIME_CASE_KINDS.REPAIR]:
    "bg-sky-100 text-sky-700 dark:bg-sky-900/30 dark:text-sky-300",
  [DOWNTIME_CASE_KINDS.ACCIDENT]:
    "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300",
  [DOWNTIME_CASE_KINDS.INSURANCE]:
    "bg-violet-100 text-violet-700 dark:bg-violet-900/30 dark:text-violet-300",
  [DOWNTIME_CASE_KINDS.IMPOUND]:
    "bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300",
};

// Default lead-time (days) added to the intake date to suggest an expected
// exit date, per case kind. Persisted/overridable per-operator via
// use-downtime-lead-days.
export const DOWNTIME_DEFAULT_LEAD_DAYS: Record<DowntimeCaseKind, number> = {
  [DOWNTIME_CASE_KINDS.REPAIR]: 7,
  [DOWNTIME_CASE_KINDS.ACCIDENT]: 30,
  [DOWNTIME_CASE_KINDS.INSURANCE]: 21,
  [DOWNTIME_CASE_KINDS.IMPOUND]: 14,
};

// ─── Inspection (осмотр) schedule triggers — mirrors backend constants/statuses.go ───
// Calendar patterns + targets reuse MAINTENANCE_CALENDAR_PATTERNS / MAINTENANCE_TARGET_TYPES
// (same generic values). Inspections are time-based only (no mileage).
export const INSPECTION_TRIGGER_TYPES = {
  DAYS: "days",
  CALENDAR: "calendar",
} as const;

export type InspectionTriggerType =
  (typeof INSPECTION_TRIGGER_TYPES)[keyof typeof INSPECTION_TRIGGER_TYPES];

export const INSPECTION_TRIGGER_LABELS: Record<string, string> = {
  [INSPECTION_TRIGGER_TYPES.DAYS]: "inspectionSchedules.triggerDays",
  [INSPECTION_TRIGGER_TYPES.CALENDAR]: "inspectionSchedules.triggerCalendar",
};

// The header's search button and the command palette are siblings under
// (dashboard)/layout.tsx, so the button reaches the palette through a window
// event rather than lifting the palette's `open` state through the layout.
export const OPEN_PALETTE_EVENT = "fleety:open-palette";


// Free-text search boxes — every list filter and every combobox picker — put
// what the operator typed straight into the GET query string. A plate, name,
// phone or document number never needs more than this; a pasted wall of text
// (a log tail, a stack trace) inflates the URI past the proxy's header limit,
// so the request dies before the API ever sees it and the browser reports a
// bare network error with no status — the screen just says «нет связи».
// api.get clamps the `search` param to this; the comboboxes cap their input.
export const MAX_SEARCH_QUERY_LEN = 128;
