#!/bin/bash
# Создаёт непривилегированного пользователя приложения и выдаёт ему права
# на уже созданную entrypoint'ом базу (POSTGRES_DB).
#
# Почему .sh, а не .sql: пароль приходит из окружения, а psql не подставляет
# переменные среды в .sql-файлы — хардкодить секрет в репозиторий нельзя.
#
# ВАЖНО: docker-entrypoint-initdb.d выполняется ТОЛЬКО при инициализации
# пустого тома. После правки этого файла нужен `docker compose down -v`.
#
# Ожидаемые переменные (маппинг задаётся в docker-compose.yml):
#   POSTGRES_USER / POSTGRES_DB — суперюзер и база, их ставит сам образ;
#   APP_DB_USER / APP_DB_PASSWORD — учётка приложения (из .env).
set -euo pipefail

: "${APP_DB_USER:?APP_DB_USER is required}"
: "${APP_DB_PASSWORD:?APP_DB_PASSWORD is required}"

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
	--set app_user="$APP_DB_USER" \
	--set app_password="$APP_DB_PASSWORD" \
	--set db_name="$POSTGRES_DB" <<-'EOSQL'
	-- :"..." подставляется как идентификатор, :'...' — как строковый литерал.
	CREATE ROLE :"app_user" LOGIN PASSWORD :'app_password';

	GRANT CONNECT ON DATABASE :"db_name" TO :"app_user";

	-- С PostgreSQL 15 схема public больше не даёт CREATE всем подряд.
	-- Без этого гранта приложение не сможет накатить миграции.
	GRANT USAGE, CREATE ON SCHEMA public TO :"app_user";
EOSQL

echo "role '$APP_DB_USER' created and granted on database '$POSTGRES_DB'"
