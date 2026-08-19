-- ============================================================================
-- 034_project_wiki_slug.sql — link each project to its wiki page
-- ============================================================================
-- Wiki: project-wiki-link. Wiki постановки живут на wiki-страницах (реестр —
-- wiki:roadmap), но связки «проект ↔ его wiki-страница» не было: из UI
-- проекта нельзя перейти в документацию, агент не может узнать, какая
-- страница принадлежит проекту. Этот миграт вводит projects.wiki_slug:
--
--   * TEXT NULL — пустое значение означает «связки нет».
--   * FK на wiki_pages.slug, ON DELETE SET NULL — удаление wiki-страницы
--     снимает привязку, но не ломает проект (просто остаётся
--     «сиротой» до ручного перепривязывания).
--   * Индекс — нужен для autocomplete в настройках проекта (WHERE slug
--     LIKE 'foo%'); FK UNIQUE на wiki_pages.slug уже покрывает точечный
--     поиск, но без отдельного индекса prefix-Лайк не использует его.
--
-- Семантика NULL vs '': API нормализует пустую строку к NULL на запись
-- (handler patchProjectHandler и agentPatchProjectHandler), и хранилище
-- возвращает NULL как пустую строку через sql.NullString. На UI поле
-- отображается как «не задано» когда NULL и принимает пустую строку как
-- «очистить».

ALTER TABLE projects ADD COLUMN wiki_slug TEXT
    REFERENCES wiki_pages(slug) ON DELETE SET NULL;

CREATE INDEX idx_projects_wiki_slug ON projects(wiki_slug);