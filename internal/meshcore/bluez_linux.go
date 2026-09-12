//go:build linux

package meshcore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	bluezName              = "org.bluez"
	deviceInterface        = "org.bluez.Device1"
	gattCharInterface      = "org.bluez.GattCharacteristic1"
	gattServiceInterface   = "org.bluez.GattService1"
	adapterInterface       = "org.bluez.Adapter1"
	propertiesInterface    = "org.freedesktop.DBus.Properties"
	objectManagerInterface = "org.freedesktop.DBus.ObjectManager"
	agentManagerInterface  = "org.bluez.AgentManager1"
)

type bluezBackend struct{ conn *dbus.Conn }

func newSystemBLEBackend() BLEBackend {
	return &bluezBackend{}
}

func (b *bluezBackend) system() (*dbus.Conn, error) {
	if b.conn != nil {
		return b.conn, nil
	}
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("%w: connect system D-Bus", ErrBLEConfiguration)
	}
	b.conn = conn
	return conn, nil
}

func (b *bluezBackend) Discover(ctx context.Context, adapter string) ([]BLEDevice, error) {
	conn, err := b.system()
	if err != nil {
		return nil, err
	}
	adapterPath := bluezAdapterPath(adapter)
	adapterObject := conn.Object(bluezName, adapterPath)
	filter := map[string]dbus.Variant{"UUIDs": dbus.MakeVariant([]string{NUSServiceUUID})}
	if call := adapterObject.CallWithContext(ctx, adapterInterface+".SetDiscoveryFilter", 0, filter); call.Err != nil {
		return nil, fmt.Errorf("%w: set NUS discovery filter", ErrBLEConfiguration)
	}
	if call := adapterObject.CallWithContext(ctx, adapterInterface+".StartDiscovery", 0); call.Err != nil {
		return nil, fmt.Errorf("%w: start discovery", ErrBLEConfiguration)
	}
	defer adapterObject.Call(adapterInterface+".StopDiscovery", 0)
	if err := waitForDiscovery(ctx); err != nil {
		return nil, err
	}
	objects, err := managedObjects(conn)
	if err != nil {
		return nil, err
	}
	devices := make([]BLEDevice, 0)
	for path, interfaces := range objects {
		if !strings.HasPrefix(string(path), string(adapterPath)+"/") {
			continue
		}
		props, ok := interfaces[deviceInterface]
		if !ok || !hasUUID(props, "UUIDs", NUSServiceUUID) {
			continue
		}
		devices = append(devices, deviceFromProps(props))
	}
	return devices, nil
}

func waitForDiscovery(ctx context.Context) error {
	// BlueZ discovery is asynchronous. Give its object cache one short,
	// cancellable turn without creating a new adapter lifecycle state.
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (b *bluezBackend) Connect(ctx context.Context, cfg BLEConfig) (BLEConnection, error) {
	conn, err := b.system()
	if err != nil {
		return nil, err
	}
	path, props, err := findBluezDevice(conn, cfg)
	if err != nil {
		return nil, err
	}
	device := conn.Object(bluezName, path)
	if !variantBool(props, "Connected") {
		if call := device.CallWithContext(ctx, deviceInterface+".Connect", 0); call.Err != nil {
			return nil, fmt.Errorf("%w: connect selected BLE peer", ErrBLEConfiguration)
		}
	}
	props, err = waitDeviceReady(ctx, conn, path)
	if err != nil {
		return nil, err
	}
	if !variantBool(props, "Paired") {
		return nil, fmt.Errorf("%w: selected BLE peer is not bonded", ErrBLEConfiguration)
	}
	objects, err := managedObjects(conn)
	if err != nil {
		return nil, err
	}
	rxPath, txPath, rxProps, txProps, ok := findNUSCharacteristics(objects, path)
	if !ok {
		return nil, fmt.Errorf("%w: MeshCore NUS service or characteristics are missing", ErrBLEConfiguration)
	}
	if !hasFlag(rxProps, "write") || !hasFlag(txProps, "notify") {
		return nil, fmt.Errorf("%w: MeshCore NUS characteristics do not support request write/notify", ErrBLEConfiguration)
	}
	mtu := variantUint16(txProps, "MTU")
	if mtu == 0 {
		mtu = variantUint16(rxProps, "MTU")
	}
	if mtu == 0 {
		return nil, fmt.Errorf("%w: BlueZ did not report negotiated characteristic MTU", ErrBLEConfiguration)
	}
	link := &bluezConnection{conn: conn, devicePath: path, device: deviceFromProps(props), rx: conn.Object(bluezName, rxPath), tx: conn.Object(bluezName, txPath), txPath: txPath, mtu: mtu, done: make(chan struct{})}
	if err := link.startNotify(ctx); err != nil {
		return nil, err
	}
	return link, nil
}

func (b *bluezBackend) Pair(ctx context.Context, cfg BLEConfig, pin string) error {
	conn, err := b.system()
	if err != nil {
		return err
	}
	path, _, err := findBluezDevice(conn, cfg)
	if err != nil {
		return err
	}
	agentPath := dbus.ObjectPath(fmt.Sprintf("/io/loramapr/receiver/meshcore/agent/%d", time.Now().UnixNano()))
	agent := &bluezPairAgent{pin: pin, device: path}
	if err := conn.Export(agent, agentPath, "org.bluez.Agent1"); err != nil {
		return err
	}
	defer conn.Export(nil, agentPath, "org.bluez.Agent1")
	defer agent.clear()
	manager := conn.Object(bluezName, "/org/bluez")
	if call := manager.CallWithContext(ctx, agentManagerInterface+".RegisterAgent", 0, agentPath, "KeyboardOnly"); call.Err != nil {
		return fmt.Errorf("%w: register temporary BlueZ pairing agent", ErrBLEPairingFailed)
	}
	defer manager.Call(agentManagerInterface+".UnregisterAgent", 0, agentPath)
	call := conn.Object(bluezName, path).CallWithContext(ctx, deviceInterface+".Pair", 0)
	if call.Err != nil {
		_ = conn.Object(bluezName, path).Call(deviceInterface+".CancelPairing", 0).Err
		return ErrBLEPairingFailed
	}
	return nil
}

func (b *bluezBackend) Forget(ctx context.Context, cfg BLEConfig) error {
	conn, err := b.system()
	if err != nil {
		return err
	}
	path, _, err := findBluezDevice(conn, cfg)
	if err != nil {
		return err
	}
	call := conn.Object(bluezName, bluezAdapterPath(cfg.Adapter)).CallWithContext(ctx, adapterInterface+".RemoveDevice", 0, path)
	if call.Err != nil {
		return fmt.Errorf("%w: remove selected BLE peer", ErrBLEConfiguration)
	}
	return nil
}

func bluezAdapterPath(adapter string) dbus.ObjectPath {
	return dbus.ObjectPath("/org/bluez/" + strings.TrimSpace(adapter))
}

type managed map[dbus.ObjectPath]map[string]map[string]dbus.Variant

func managedObjects(conn *dbus.Conn) (managed, error) {
	var objects managed
	call := conn.Object(bluezName, "/").Call(objectManagerInterface+".GetManagedObjects", 0)
	if call.Err != nil {
		return nil, fmt.Errorf("%w: inspect BlueZ object manager", ErrBLEConfiguration)
	}
	if err := call.Store(&objects); err != nil {
		return nil, err
	}
	return objects, nil
}
func findBluezDevice(conn *dbus.Conn, cfg BLEConfig) (dbus.ObjectPath, map[string]dbus.Variant, error) {
	objects, err := managedObjects(conn)
	if err != nil {
		return "", nil, err
	}
	adapter := bluezAdapterPath(cfg.Adapter)
	for path, interfaces := range objects {
		props, ok := interfaces[deviceInterface]
		if ok && strings.HasPrefix(string(path), string(adapter)+"/") && strings.EqualFold(variantString(props, "Address"), cfg.PeerAddress) {
			return path, props, nil
		}
	}
	return "", nil, fmt.Errorf("%w: selected BLE peer is unavailable", ErrBLEConfiguration)
}
func waitDeviceReady(ctx context.Context, conn *dbus.Conn, path dbus.ObjectPath) (map[string]dbus.Variant, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		objects, err := managedObjects(conn)
		if err != nil {
			return nil, err
		}
		props := objects[path][deviceInterface]
		if variantBool(props, "Connected") && variantBool(props, "ServicesResolved") {
			return props, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
func findNUSCharacteristics(objects managed, devicePath dbus.ObjectPath) (dbus.ObjectPath, dbus.ObjectPath, map[string]dbus.Variant, map[string]dbus.Variant, bool) {
	var rx, tx dbus.ObjectPath
	var rxProps, txProps map[string]dbus.Variant
	for path, interfaces := range objects {
		if !strings.HasPrefix(string(path), string(devicePath)+"/") {
			continue
		}
		props, ok := interfaces[gattCharInterface]
		if !ok {
			continue
		}
		servicePath := variantObjectPath(props, "Service")
		service, ok := objects[servicePath][gattServiceInterface]
		if !ok || !strings.EqualFold(variantString(service, "UUID"), NUSServiceUUID) {
			continue
		}
		switch strings.ToLower(variantString(props, "UUID")) {
		case NUSRXUUID:
			rx, rxProps = path, props
		case NUSTXUUID:
			tx, txProps = path, props
		}
	}
	return rx, tx, rxProps, txProps, rx != "" && tx != ""
}
func variantString(props map[string]dbus.Variant, key string) string {
	if value, ok := props[key]; ok {
		if text, ok := value.Value().(string); ok {
			return text
		}
	}
	return ""
}
func variantBool(props map[string]dbus.Variant, key string) bool {
	if value, ok := props[key]; ok {
		if result, ok := value.Value().(bool); ok {
			return result
		}
	}
	return false
}
func variantUint16(props map[string]dbus.Variant, key string) uint16 {
	if value, ok := props[key]; ok {
		switch value := value.Value().(type) {
		case uint16:
			return value
		case uint32:
			return uint16(value)
		}
	}
	return 0
}
func variantObjectPath(props map[string]dbus.Variant, key string) dbus.ObjectPath {
	if value, ok := props[key]; ok {
		if path, ok := value.Value().(dbus.ObjectPath); ok {
			return path
		}
	}
	return ""
}
func hasUUID(props map[string]dbus.Variant, key, want string) bool {
	value, ok := props[key]
	if !ok {
		return false
	}
	values, ok := value.Value().([]string)
	if !ok {
		return false
	}
	for _, item := range values {
		if strings.EqualFold(item, want) {
			return true
		}
	}
	return false
}
func hasFlag(props map[string]dbus.Variant, want string) bool { return hasUUID(props, "Flags", want) }
func deviceFromProps(props map[string]dbus.Variant) BLEDevice {
	return BLEDevice{Address: strings.ToUpper(variantString(props, "Address")), Name: variantString(props, "Name"), Bonded: variantBool(props, "Paired"), Connected: variantBool(props, "Connected")}
}

type bluezConnection struct {
	conn       *dbus.Conn
	devicePath dbus.ObjectPath
	device     BLEDevice
	rx, tx     dbus.BusObject
	txPath     dbus.ObjectPath
	mtu        uint16
	signals    chan *dbus.Signal
	closeOnce  sync.Once
	done       chan struct{}
}

func (c *bluezConnection) Device() BLEDevice   { return c.device }
func (c *bluezConnection) MTU() uint16         { return c.mtu }
func (*bluezConnection) HasNUS() bool          { return true }
func (*bluezConnection) CanWriteRequest() bool { return true }
func (*bluezConnection) CanNotify() bool       { return true }
func (c *bluezConnection) startNotify(ctx context.Context) error {
	c.signals = make(chan *dbus.Signal, 16)
	c.conn.Signal(c.signals)
	rule := fmt.Sprintf("type='signal',interface='%s',member='PropertiesChanged',path='%s'", propertiesInterface, c.txPath)
	if call := c.conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.AddMatch", 0, rule); call.Err != nil {
		return call.Err
	}
	if call := c.tx.CallWithContext(ctx, gattCharInterface+".StartNotify", 0); call.Err != nil {
		return fmt.Errorf("%w: start NUS notifications", ErrBLEConfiguration)
	}
	return nil
}
func (c *bluezConnection) ReadNotification(ctx context.Context) ([]byte, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.done:
			return nil, errors.New("meshcore BLE link closed")
		case signal, ok := <-c.signals:
			if !ok {
				return nil, errors.New("BlueZ notification stream closed")
			}
			if signal == nil || signal.Path != c.txPath || signal.Name != propertiesInterface+".PropertiesChanged" || len(signal.Body) < 2 {
				continue
			}
			iface, _ := signal.Body[0].(string)
			changed, _ := signal.Body[1].(map[string]dbus.Variant)
			if iface != gattCharInterface {
				continue
			}
			value, ok := changed["Value"]
			if !ok {
				continue
			}
			payload, ok := value.Value().([]byte)
			if !ok {
				return nil, fmt.Errorf("%w: BlueZ notification value type", ErrBLEFrameInvalid)
			}
			return append([]byte(nil), payload...), nil
		}
	}
}
func (c *bluezConnection) WriteRequest(ctx context.Context, payload []byte) error {
	opts := map[string]dbus.Variant{"type": dbus.MakeVariant("request")}
	if call := c.rx.CallWithContext(ctx, gattCharInterface+".WriteValue", 0, payload, opts); call.Err != nil {
		return fmt.Errorf("%w: write NUS request", ErrBLEConfiguration)
	}
	return nil
}
func (c *bluezConnection) Close() error {
	var err error
	c.closeOnce.Do(func() {
		if c.tx != nil {
			err = c.tx.Call(gattCharInterface+".StopNotify", 0).Err
		}
		if c.signals != nil {
			c.conn.RemoveSignal(c.signals)
		}
		close(c.done)
	})
	return err
}

type bluezPairAgent struct {
	mu     sync.Mutex
	pin    string
	device dbus.ObjectPath
}

func (a *bluezPairAgent) RequestPasskey(device dbus.ObjectPath) (uint32, *dbus.Error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if device != a.device || len(a.pin) != 6 {
		return 0, dbus.NewError("org.bluez.Error.Rejected", []interface{}{"pairing rejected"})
	}
	var value uint32
	for _, digit := range a.pin {
		value = value*10 + uint32(digit-'0')
	}
	return value, nil
}
func (a *bluezPairAgent) RequestPinCode(device dbus.ObjectPath) (string, *dbus.Error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if device != a.device {
		return "", dbus.NewError("org.bluez.Error.Rejected", []interface{}{"pairing rejected"})
	}
	return a.pin, nil
}
func (*bluezPairAgent) DisplayPinCode(dbus.ObjectPath, string) *dbus.Error         { return nil }
func (*bluezPairAgent) DisplayPasskey(dbus.ObjectPath, uint32, uint16) *dbus.Error { return nil }
func (*bluezPairAgent) RequestConfirmation(dbus.ObjectPath, uint32) *dbus.Error {
	return dbus.NewError("org.bluez.Error.Rejected", []interface{}{"confirmation unsupported"})
}
func (*bluezPairAgent) RequestAuthorization(dbus.ObjectPath) *dbus.Error { return nil }
func (a *bluezPairAgent) AuthorizeService(device dbus.ObjectPath, uuid string) *dbus.Error {
	if device == a.device && strings.EqualFold(uuid, NUSServiceUUID) {
		return nil
	}
	return dbus.NewError("org.bluez.Error.Rejected", []interface{}{"service not authorized"})
}
func (*bluezPairAgent) Cancel() *dbus.Error { return nil }
func (a *bluezPairAgent) clear()            { a.mu.Lock(); a.pin = ""; a.mu.Unlock() }
