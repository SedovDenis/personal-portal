"use strict";

// Small dependency-free editor. Data is stored as an HTML fragment in notes.content.
// Never insert database content as trusted HTML: reconstruct safe elements first.
(() => {
    const allowed = new Set(['P', 'DIV', 'BR', 'STRONG', 'B', 'EM', 'I', 'U', 'S', 'H2', 'H3',
        'UL', 'OL', 'LI', 'BLOCKQUOTE', 'HR', 'A', 'SPAN', 'INPUT']);
    const forbidden = new Set(['SCRIPT', 'STYLE', 'SVG', 'MATH', 'IFRAME', 'OBJECT', 'EMBED', 'FORM', 'TEMPLATE',
        'IMG', 'VIDEO', 'AUDIO', 'LINK', 'META', 'BASE']);
    const allowedClasses = new Set(['note-checkline', 'note-radiogroup']);

    function cleanFragment(input) {
        const parsed = new DOMParser().parseFromString(input, 'text/html');
        let count = 0;
        function visit(node, depth) {
            if (++count > 20000 || depth > 35) return document.createDocumentFragment();
            if (node.nodeType === Node.TEXT_NODE) return document.createTextNode(node.nodeValue || '');
            if (node.nodeType !== Node.ELEMENT_NODE) return document.createDocumentFragment();
            const tag = node.tagName.toUpperCase();
            if (forbidden.has(tag)) return document.createDocumentFragment();
            const el = allowed.has(tag) ? document.createElement(tag.toLowerCase()) : document.createDocumentFragment();
            if (tag === 'INPUT') {
                const typ = (node.getAttribute('type') || '').toLowerCase();
                if (!['checkbox', 'radio'].includes(typ)) return document.createDocumentFragment();
                el.type = typ;
                el.setAttribute('contenteditable', 'false');
                if (node.hasAttribute('checked')) el.setAttribute('checked', '');
                const name = (node.getAttribute('name') || '').slice(0, 80);
                if (typ === 'radio' && /^[\w-]+$/.test(name)) el.setAttribute('name', name);
                return el;
            }
            if (tag === 'A') {
                const href = node.getAttribute('href') || '';
                if (/^(https?:\/\/|mailto:)/i.test(href)) {
                    el.setAttribute('href', href);
                    el.setAttribute('rel', 'noopener noreferrer');
                    el.setAttribute('target', '_blank');
                }
            }
            const cls = node.classList;
            if (cls) for (const c of cls) if (allowedClasses.has(c) && el.nodeType === Node.ELEMENT_NODE) el.classList.add(c);
            for (const child of node.childNodes) el.appendChild(visit(child, depth + 1));
            return el;
        }
        const output = document.createElement('div');
        for (const item of parsed.body.childNodes) output.appendChild(visit(item, 0));
        return output;
    }

    function normalizeSource(value) {
        const trimmed = value.trim();
        if (!trimmed) return '<p></p>';
        // Old notes were plain text. Keep their text, never interpret it as HTML.
        if (!/^<(p|div|ul|ol|h2|h3|blockquote|hr|strong|b|em|i|span|br|input|a)\b/i.test(trimmed)) {
            const plain = document.createElement('div');
            for (const line of value.split('\n')) {
                const p = document.createElement('p');
                p.textContent = line;
                plain.appendChild(p);
            }
            return plain.innerHTML;
        }
        return cleanFragment(value).innerHTML;
    }

    function prepareEditor(form) {
        const editor = form.querySelector('.rich-editor');
        const source = form.querySelector('.rich-content-source');
        const toolbar = form.querySelector('.editor-toolbar');
        if (!editor || !source || !toolbar) return;
        editor.replaceChildren(...Array.from(cleanFragment(normalizeSource(source.value)).childNodes));
        editor.setAttribute('spellcheck', 'true');

        const buttons = [
            ['B', 'Жирный', 'bold'], ['I', 'Курсив', 'italic'], ['U', 'Подчеркнуть', 'underline'],
            ['H2', 'Заголовок', 'heading'], ['• Список', 'Маркированный список', 'ul'],
            ['1. Список', 'Нумерованный список', 'ol'],
            ['☑ Чекбокс', 'Добавить пункт чек-листа', 'check'],
            ['◉ Выбор', 'Добавить группу вариантов с радиокнопками', 'radio'],
            ['—', 'Разделитель', 'hr'], ['🔗', 'Добавить ссылку', 'link'],
        ];
        let savedRange = null;
        function saveRange() {
            const sel = window.getSelection();
            if (sel && sel.rangeCount && editor.contains(sel.anchorNode)) savedRange = sel.getRangeAt(0).cloneRange();
        }
        function restoreRange() {
            editor.focus();
            if (savedRange) { const sel = window.getSelection(); sel.removeAllRanges(); sel.addRange(savedRange); }
        }
        editor.addEventListener('keyup', saveRange);
        editor.addEventListener('mouseup', saveRange);
        function insertHTML(markup) {
            restoreRange();
            document.execCommand('insertHTML', false, markup);
            saveRange();
        }
        for (const [label, title, command] of buttons) {
            const button = document.createElement('button');
            button.type = 'button';
            button.textContent = label;
            button.title = title;
            button.setAttribute('aria-label', title);
            button.addEventListener('mousedown', e => e.preventDefault());
            button.addEventListener('click', () => {
                restoreRange();
                if (command === 'heading') document.execCommand('formatBlock', false, 'h2');
                else if (command === 'ul') document.execCommand('insertUnorderedList');
                else if (command === 'ol') document.execCommand('insertOrderedList');
                else if (command === 'check') insertHTML('<div class="note-checkline"><input type="checkbox" contenteditable="false"><span>Новый пункт</span></div><p></p>');
                else if (command === 'radio') {
                    const group = `choice-${Math.random().toString(36).slice(2, 11)}`;
                    insertHTML(`<div class="note-radiogroup"><div class="note-checkline"><input type="radio" name="${group}" contenteditable="false"><span>Вариант 1</span></div><div class="note-checkline"><input type="radio" name="${group}" contenteditable="false"><span>Вариант 2</span></div></div><p></p>`);
                }
                else if (command === 'hr') insertHTML('<hr><p></p>');
                else if (command === 'link') {
                    const url = prompt('Адрес ссылки (https://...)');
                    if (url && /^https?:\/\//i.test(url)) document.execCommand('createLink', false, url);
                    else if (url) alert('Разрешены только ссылки http:// и https://');
                }
                else document.execCommand(command);
                saveRange();
                editor.dispatchEvent(new Event('input', {bubbles:true}));
            });
            toolbar.appendChild(button);
        }
        function snapshot() {
            // Persist state of checkboxes and radio buttons in the HTML fragment.
            for (const input of editor.querySelectorAll('input[type="checkbox"],input[type="radio"]')) {
                if (input.checked) input.setAttribute('checked', '');
                else input.removeAttribute('checked');
            }
            return cleanFragment(editor.innerHTML).innerHTML;
        }
        const status = form.querySelector('.draft-status');
        const kind = form.dataset.draftKind;
        const targetID = form.dataset.draftId;
        let saveTimer;
        let changed = false;
        let saving = false;
        let pending = false;
        let publishing = false;
        let activeSave = null;
        async function saveDraft() {
            if (publishing || !changed || saving || !kind) {if (saving && !publishing) pending = true;return;}
            changed = false;
            saving = true;
            status && (status.textContent = 'Сохраняем черновик…');
            const fields = new URLSearchParams();
            fields.set('csrf_token', form.querySelector('[name="csrf_token"]').value);
            fields.set('kind', kind);
            fields.set('target_id', targetID);
            fields.set('title', (form.querySelector('[name="title"], [name="name"]') || {}).value || '');
            fields.set('folder_id', (form.querySelector('[name="folder_id"]') || {}).value || '');
            fields.set('content', snapshot());
            try {
                activeSave = fetch('/notes/drafts', {
                    method:'POST', credentials:'same-origin',
                    headers:{'Content-Type':'application/x-www-form-urlencoded'},body:fields.toString()
                });
                const response = await activeSave;
                if (!response.ok) throw new Error(String(response.status));
                status && (status.textContent = 'Черновик сохранён');
            } catch (_) {
                changed = true;
                status && (status.textContent = 'Не удалось сохранить черновик. Проверьте соединение.');
            } finally {
                saving = false;
                activeSave = null;
                if (!publishing && (pending || changed)) {
                    pending = false;
                    clearTimeout(saveTimer);
                    saveTimer = setTimeout(saveDraft, 1500);
                }
            }
        }
        function scheduleDraft() {
            changed = true;
            if (status) status.textContent = 'Есть несохранённые изменения';
            clearTimeout(saveTimer);
            saveTimer = setTimeout(saveDraft, 900);
        }
        editor.addEventListener('input', scheduleDraft);
        editor.addEventListener('change', scheduleDraft);
        form.querySelectorAll('[name="title"], [name="name"], [name="folder_id"]').forEach(el => {
            el.addEventListener('input', scheduleDraft);
            el.addEventListener('change', scheduleDraft);
        });
        form.addEventListener('submit', async (event) => {
            event.preventDefault();
            if (publishing) return;
            clearTimeout(saveTimer);
            publishing = true;
            // Wait for an earlier autosave, then persist the final version BEFORE publishing.
            // This protects the user's text even when a shared note causes a version conflict.
            if (activeSave) {
                try { await activeSave; } catch (_) { /* Try once more with the latest text. */ }
            }
            const fields = new URLSearchParams();
            fields.set('csrf_token', form.querySelector('[name="csrf_token"]').value);
            fields.set('kind', kind);
            fields.set('target_id', targetID);
            fields.set('title', (form.querySelector('[name="title"], [name="name"]') || {}).value || '');
            fields.set('folder_id', (form.querySelector('[name="folder_id"]') || {}).value || '');
            source.value = snapshot();
            fields.set('content', source.value);
            try {
                const response = await fetch('/notes/drafts', {
                    method:'POST', credentials:'same-origin',
                    headers:{'Content-Type':'application/x-www-form-urlencoded'},
                    body:fields.toString()
                });
                if (!response.ok) throw new Error(String(response.status));
                changed = false;
                form.submit();
            } catch (_) {
                publishing = false;
                changed = true;
                if (status) status.textContent = 'Черновик не сохранился. Проверьте соединение и повторите сохранение.';
            }
        });
        window.addEventListener('pagehide', () => {
            if (publishing || !changed || !navigator.sendBeacon) return;
            const fields = new URLSearchParams();
            fields.set('csrf_token', form.querySelector('[name="csrf_token"]').value);
            fields.set('kind', kind);
            fields.set('target_id', targetID);
            fields.set('title', (form.querySelector('[name="title"], [name="name"]') || {}).value || '');
            fields.set('folder_id', (form.querySelector('[name="folder_id"]') || {}).value || '');
            fields.set('content', snapshot());
            navigator.sendBeacon('/notes/drafts', new Blob([fields.toString()], {type:'application/x-www-form-urlencoded'}));
        });
    }
    document.querySelectorAll('.rich-note-form').forEach(prepareEditor);

    // Display stored content only in view mode. Reconstruct a safe fragment; never inject raw DB HTML.
    document.querySelectorAll('.note-readonly-content').forEach(view => {
        const source = view.parentElement.querySelector('.note-readonly-source');
        if (!source) return;
        const fragment = cleanFragment(normalizeSource(source.value));
        for (const control of fragment.querySelectorAll('input[type="checkbox"], input[type="radio"]')) {
            control.disabled = true;
        }
        view.replaceChildren(...Array.from(fragment.childNodes));
    });

    const dialog = document.getElementById('folder-dialog');
    const folderForm = document.getElementById('folder-form');
    const folderDelete = document.getElementById('folder-delete-form');
    if (dialog && folderForm && folderDelete) {
        const id = document.getElementById('folder-id');
        const name = document.getElementById('folder-name');
        const parent = document.getElementById('folder-parent');
        const favoriteFlag = document.getElementById('folder-favorite');
        const sharedFlag = document.getElementById('folder-shared');
        const shareUsers = document.getElementById('folder-share-users');
        const open = () => typeof dialog.showModal === 'function' ? dialog.showModal() : dialog.setAttribute('open', '');
        document.getElementById('new-folder')?.addEventListener('click', () => {
            folderForm.action = '/folders';
            document.getElementById('folder-dialog-title').textContent = 'Новая папка';
            folderForm.reset(); id.value = ''; folderDelete.hidden = true;
            for (const option of shareUsers.options) option.selected = false;
            for (const option of parent.options) option.hidden = false;
            open(); name.focus();
        });
        document.querySelectorAll('.folder-settings').forEach(button => {
            button.addEventListener('click', () => {
                folderForm.action = '/folders/update';
                document.getElementById('folder-dialog-title').textContent = 'Изменить папку';
                id.value = button.dataset.folderId;
                name.value = button.dataset.folderName;
                parent.value = button.dataset.folderParent || '';
                favoriteFlag.checked = button.dataset.folderFavorite === 'true';
                sharedFlag.checked = button.dataset.folderShared === 'true';
                const selected = new Set((button.dataset.folderSharedWith || '').split(','));
                for (const option of shareUsers.options) option.selected = selected.has(option.value);
                for (const option of parent.options) option.hidden = option.value === id.value;
                document.getElementById('folder-delete-id').value = id.value;
                folderDelete.hidden = false;
                open(); name.focus();
            });
        });
        document.getElementById('folder-cancel')?.addEventListener('click', () => dialog.close());
        dialog.addEventListener('click', event => { if (event.target === dialog) dialog.close(); });
    }

    document.querySelectorAll('.confirm-delete').forEach(form => {
        form.addEventListener('submit', event => {
            if (!window.confirm(form.dataset.confirm || 'Подтвердить удаление?')) event.preventDefault();
        });
    });
    const bulkForm = document.getElementById('notes-bulk-form');
    if (bulkForm) {
        const checks = [...bulkForm.querySelectorAll('input[name="ids"]')];
        const selectAll = document.getElementById('select-all-notes');
        const op = document.getElementById('bulk-operation');
        const target = document.getElementById('bulk-folder');
        selectAll?.addEventListener('change', () => { for (const check of checks) check.checked = selectAll.checked; });
        function mode() {target.hidden = op.value === 'delete';}
        op?.addEventListener('change', mode); mode();
        bulkForm.addEventListener('submit', event => {
            if (!checks.some(check => check.checked)) { alert('Выберите хотя бы одну заметку'); event.preventDefault(); return; }
            if (op.value === 'move' && !target.value) { alert('Выберите папку назначения'); event.preventDefault(); return; }
            if (op.value === 'delete') {
                if (!confirm('Удалить все выбранные заметки? Отменить это действие невозможно.')) {event.preventDefault();return;}
                document.getElementById('bulk-confirm').value = 'yes';
            }
        });
    }
})();
