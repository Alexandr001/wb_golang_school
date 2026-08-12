'use strict';

// Без фреймворков и сборки: один fetch к /order/{uid} и ручной рендер.
// Данные с сервера кладём только через textContent — innerHTML дал бы XSS.

const SAMPLE_UID = 'b563feb7b2b84b6test';

const form = document.getElementById('search-form');
const input = document.getElementById('order-uid');
const searchButton = document.getElementById('search-button');
const sampleButton = document.getElementById('sample-button');
const statusBox = document.getElementById('status');
const result = document.getElementById('result');
const meta = document.getElementById('meta');
const itemsBody = document.getElementById('items-body');
const itemsCount = document.getElementById('items-count');

// Отличаем свой переход по хэшу (ставим его сами при поиске) от чужого:
// «назад», правка адреса руками.
let lastRequested = null;

form.addEventListener('submit', (event) => {
    event.preventDefault();

    const uid = input.value.trim();
    if (uid === '') {
        showError('Введите идентификатор заказа.');
        return;
    }

    // Хэш делает результат перезагружаемым: после F5 видно, что заказ пришёл из кэша.
    window.location.hash = encodeURIComponent(uid);
    request(uid);
});

sampleButton.addEventListener('click', () => {
    input.value = SAMPLE_UID;
    input.focus();
});

window.addEventListener('hashchange', () => {
    const uid = uidFromHash();
    if (uid !== '' && uid !== lastRequested) {
        input.value = uid;
        request(uid);
    }
});

const initialUID = uidFromHash();
if (initialUID !== '') {
    input.value = initialUID;
    request(initialUID);
}

function request(uid) {
    lastRequested = uid;
    search(uid);
}

function uidFromHash() {
    try {
        return decodeURIComponent(window.location.hash.slice(1)).trim();
    } catch (error) {
        // Битый процент-энкодинг в адресе — не повод падать всей странице.
        return '';
    }
}

async function search(uid) {
    setBusy(true);
    showInfo('Запрашиваем заказ…');

    const startedAt = performance.now();

    try {
        const response = await fetch('/order/' + encodeURIComponent(uid), {
            headers: {'Accept': 'application/json'},
        });
        const payload = await readJSON(response);
        const elapsed = performance.now() - startedAt;

        if (!response.ok) {
            showError(errorText(response.status, payload));
            return;
        }

        render(payload, response.headers.get('X-Cache'), elapsed);
    } catch (error) {
        // Сюда попадают только сетевые сбои: HTTP-ошибки разобраны выше.
        showError('Сервис недоступен: ' + error.message);
    } finally {
        setBusy(false);
    }
}

async function readJSON(response) {
    try {
        return await response.json();
    } catch (error) {
        return null;
    }
}

function errorText(status, payload) {
    if (status === 404) {
        return 'Заказ не найден. Проверьте идентификатор — возможно, он ещё не приехал из Kafka.';
    }
    if (payload && typeof payload.error === 'string') {
        return 'Ошибка ' + status + ': ' + payload.error;
    }
    return 'Ошибка ' + status + '.';
}

function render(order, cacheHeader, elapsed) {
    hideStatus();

    renderMeta(order, cacheHeader, elapsed);

    fill('order-fields', [
        ['order_uid', order.order_uid],
        ['track_number', order.track_number],
        ['entry', order.entry],
        ['customer_id', order.customer_id],
        ['delivery_service', order.delivery_service],
        ['locale', order.locale],
        ['internal_signature', order.internal_signature || '—'],
        ['shardkey', order.shardkey],
        ['sm_id', order.sm_id],
        ['oof_shard', order.oof_shard],
        ['date_created', formatDate(order.date_created)],
    ]);

    const delivery = order.delivery || {};
    fill('delivery-fields', [
        ['Получатель', delivery.name],
        ['Телефон', delivery.phone],
        ['E-mail', delivery.email],
        ['Индекс', delivery.zip],
        ['Город', delivery.city],
        ['Адрес', delivery.address],
        ['Регион', delivery.region || '—'],
    ]);

    const payment = order.payment || {};
    fill('payment-fields', [
        ['transaction', payment.transaction],
        ['request_id', payment.request_id || '—'],
        ['Провайдер', payment.provider],
        ['Банк', payment.bank],
        ['Валюта', payment.currency],
        ['Сумма', formatNumber(payment.amount)],
        ['Товары', formatNumber(payment.goods_total)],
        ['Доставка', formatNumber(payment.delivery_cost)],
        ['Пошлина', formatNumber(payment.custom_fee)],
        ['Дата оплаты', formatUnix(payment.payment_dt)],
    ]);

    renderItems(order.items || []);

    result.hidden = false;
}

function renderMeta(order, cacheHeader, elapsed) {
    meta.replaceChildren();

    const fromCache = cacheHeader === 'HIT';
    const badge = document.createElement('span');
    badge.className = 'badge ' + (fromCache ? 'badge--hit' : 'badge--miss');
    badge.textContent = fromCache ? 'X-Cache: HIT' : 'X-Cache: MISS';
    meta.append(badge);

    const timing = document.createElement('span');
    timing.textContent = 'ответ за ' + elapsed.toFixed(1) + ' мс';
    meta.append(timing);

    const source = document.createElement('span');
    source.textContent = fromCache ? '· отдан из памяти' : '· поднят из базы и положен в кэш';
    meta.append(source);
}

function renderItems(items) {
    itemsCount.textContent = '(' + items.length + ')';
    itemsBody.replaceChildren();

    for (const item of items) {
        const row = document.createElement('tr');
        appendCell(row, item.name);
        appendCell(row, item.brand);
        appendCell(row, item.size);
        appendCell(row, formatNumber(item.price), true);
        appendCell(row, item.sale + '%', true);
        appendCell(row, formatNumber(item.total_price), true);
        appendCell(row, item.chrt_id, true);
        appendCell(row, item.nm_id, true);
        appendCell(row, item.status, true);
        itemsBody.append(row);
    }
}

function appendCell(row, value, numeric) {
    const cell = document.createElement('td');
    if (numeric) {
        cell.className = 'num';
    }
    cell.textContent = value === undefined || value === null || value === '' ? '—' : String(value);
    row.append(cell);
}

function fill(containerID, pairs) {
    const container = document.getElementById(containerID);
    container.replaceChildren();

    for (const [label, value] of pairs) {
        const term = document.createElement('dt');
        term.textContent = label;

        const definition = document.createElement('dd');
        definition.textContent = value === undefined || value === null || value === '' ? '—' : String(value);

        container.append(term, definition);
    }
}

function formatNumber(value) {
    return typeof value === 'number' ? value.toLocaleString('ru-RU') : value;
}

function formatDate(value) {
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) {
        return value;
    }
    return value + ' (' + parsed.toLocaleString('ru-RU') + ')';
}

function formatUnix(seconds) {
    if (typeof seconds !== 'number') {
        return seconds;
    }
    return seconds + ' (' + new Date(seconds * 1000).toLocaleString('ru-RU') + ')';
}

function setBusy(busy) {
    searchButton.disabled = busy;
    searchButton.textContent = busy ? 'Ищем…' : 'Найти';
}

function showError(message) {
    result.hidden = true;
    statusBox.className = 'status';
    statusBox.textContent = message;
    statusBox.hidden = false;
}

function showInfo(message) {
    statusBox.className = 'status status--info';
    statusBox.textContent = message;
    statusBox.hidden = false;
}

function hideStatus() {
    statusBox.hidden = true;
    statusBox.textContent = '';
}
