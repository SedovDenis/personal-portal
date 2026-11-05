"use strict";
document.getElementById('another-account')?.addEventListener('click', () => {
    document.getElementById('username').value = '';
    document.getElementById('password').value = '';
    document.getElementById('username').focus();
});
