{{.Code}}

var _init, _start, _run, _stop, _handle;
(function() {
    var initData = {{.InitData}};
    // Initialization
    try {
        _init = init.bind(null, initData);
    } catch (e) {
        _init = () => {
        };
    }

    try {
        _handle = handle;
    } catch (e) {
        _handle = () => {
        };
    }
})();


export {
    _init as init,
    _handle as handle
};
