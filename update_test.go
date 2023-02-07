package corebgp

import (
	"errors"
	"net/netip"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

type updateMessageForTests struct {
	withdrawn     []netip.Prefix
	origin        uint8
	asPath        []uint32
	nextHop       netip.Addr
	nlri          []netip.Prefix
	ipv6NextHops  []netip.Addr
	ipv6NLRI      []netip.Prefix
	ipv6Withdrawn []netip.Prefix
}

func newPathAttrsDecodeFn() func(m *updateMessageForTests, code uint8, flags PathAttrFlags, b []byte) error {
	reachDecodeFn := NewMPReachNLRIDecodeFn[*updateMessageForTests](
		func(m *updateMessageForTests, afi uint16, safi uint8, nh, nlri []byte) error {
			if afi == AFI_IPV6 && safi == SAFI_UNICAST {
				nhs, err := DecodeMPReachIPv6NextHops(nh)
				if err != nil {
					return err
				}
				prefixes, err := DecodeMPIPv6Prefixes(nlri)
				if err != nil {
					return err
				}
				m.ipv6NextHops = nhs
				m.ipv6NLRI = prefixes
			}
			return nil
		},
	)
	unreachDecodeFn := NewMPUnreachNLRIDecodeFn[*updateMessageForTests](
		func(m *updateMessageForTests, afi uint16, safi uint8, withdrawn []byte) error {
			if afi == AFI_IPV6 && safi == SAFI_UNICAST {
				prefixes, err := DecodeMPIPv6Prefixes(withdrawn)
				if err != nil {
					return err
				}
				m.ipv6Withdrawn = prefixes
			}
			return nil
		},
	)
	return func(m *updateMessageForTests, code uint8, flags PathAttrFlags, b []byte) error {
		switch code {
		case PATH_ATTR_ORIGIN:
			var o OriginPathAttr
			err := o.Decode(flags, b)
			if err != nil {
				return err
			}
			m.origin = uint8(o)
			return nil
		case PATH_ATTR_AS_PATH:
			var a ASPathAttr
			err := a.Decode(flags, b)
			if err != nil {
				return err
			}
			m.asPath = a.ASSequence
			return nil
		case PATH_ATTR_NEXT_HOP:
			var nh NextHopPathAttr
			err := nh.Decode(flags, b)
			if err != nil {
				return err
			}
			m.nextHop = netip.Addr(nh)
			return nil
		case PATH_ATTR_MP_REACH_NLRI:
			return reachDecodeFn(m, flags, b)
		case PATH_ATTR_MP_UNREACH_NLRI:
			return unreachDecodeFn(m, flags, b)
		case PATH_ATTR_ATOMIC_AGGREGATE:
			var aa AtomicAggregatePathAttr
			return aa.Decode(flags, b)
		}
		return nil
	}
}

func FuzzUpdateDecoder_Decode(f *testing.F) {
	f.Add([]byte{
		0x00, 0x03, // withdrawn routes length
		0x10, 0x0a, 0x00, // withdrawn 10.0.0.0/16
		0x00, 0x14, // total path attribute length
		0x40, 0x01, 0x01, 0x01, // origin egp
		0x40, 0x02, 0x06, 0x02, 0x01, 0x00, 0x00, 0xfd, 0xea, // as_path 65002
		0x40, 0x03, 0x04, 0xc0, 0x00, 0x02, 0x02, // next_hop 192.0.2.2
		0x08, 0x0a, // nlri 10.0.0.0/8
	})
	f.Add([]byte{
		0x00, 0x00, // withdrawn routes length
		0x00, 0x3f, // total path attribute length
		// extended len MP_REACH_NLRI 2001:db8::/64 nhs 2001:db8::2 & fe80::42:c0ff:fe00:202
		0x90, 0x0e, 0x00, 0x2e, 0x00, 0x02, 0x01, 0x20, 0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xfe, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x42, 0xc0, 0xff, 0xfe, 0x00, 0x02, 0x02, 0x00, 0x40, 0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x00,
		0x40, 0x01, 0x01, 0x00, // origin igp
		0x40, 0x02, 0x06, 0x02, 0x01, 0x00, 0x00, 0xfd, 0xea, // as_path 65002
	})
	f.Add([]byte{
		0x00, 0x00, // withdrawn routes length
		0x00, 0x09, // total path attribute length
		0x90, 0x0f, 0x00, 0x05, 0x00, 0x02, 0x01, 0x07, 0xfc, // empty IPv6 MP_UNREACH_NLRI
	})
	f.Fuzz(func(t *testing.T, b []byte) {
		ud := NewUpdateDecoder[*updateMessageForTests](
			NewWithdrawnRoutesDecodeFn(func(m *updateMessageForTests, r []netip.Prefix) error {
				m.withdrawn = r
				return nil
			}),
			newPathAttrsDecodeFn(),
			NewNLRIDecodeFn(func(m *updateMessageForTests, r []netip.Prefix) error {
				m.nlri = r
				return nil
			}),
		)
		m := &updateMessageForTests{}
		ud.Decode(m, b)
	})
}

func TestUpdateDecoder_Decode(t *testing.T) {
	ud := NewUpdateDecoder[*updateMessageForTests](
		NewWithdrawnRoutesDecodeFn(func(m *updateMessageForTests, r []netip.Prefix) error {
			m.withdrawn = r
			return nil
		}),
		newPathAttrsDecodeFn(),
		NewNLRIDecodeFn(func(m *updateMessageForTests, r []netip.Prefix) error {
			m.nlri = r
			return nil
		}),
	)

	t.Run("valid data", func(t *testing.T) {
		cases := []struct {
			name     string
			toDecode []byte
			want     *updateMessageForTests
		}{
			{
				name: "IPv4 withdrawn and IPv4 nlri",
				toDecode: []byte{
					0x00, 0x03, // withdrawn routes length
					0x10, 0x0a, 0x00, // withdrawn 10.0.0.0/16
					0x00, 0x14, // total path attribute length
					0x40, 0x01, 0x01, 0x01, // origin egp
					0x40, 0x02, 0x06, 0x02, 0x01, 0x00, 0x00, 0xfd, 0xea, // as_path 65002
					0x40, 0x03, 0x04, 0xc0, 0x00, 0x02, 0x02, // next_hop 192.0.2.2
					0x08, 0x0a, // nlri 10.0.0.0/8
				},
				want: &updateMessageForTests{
					withdrawn: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/16")},
					origin:    1,
					asPath:    []uint32{65002},
					nextHop:   netip.MustParseAddr("192.0.2.2"),
					nlri:      []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
				},
			},
			{
				name: "MP_UNREACH_NLRI IPv6 prefix",
				toDecode: []byte{
					0x00, 0x00, // withdrawn routes length
					0x00, 0x09, // total path attribute length
					0x90, 0x0f, 0x00, 0x05, 0x00, 0x02, 0x01, 0x07, 0xfc, // extended len IPv6 MP_UNREACH_NLRI fc00::/7
				},
				want: &updateMessageForTests{
					ipv6Withdrawn: []netip.Prefix{
						netip.MustParsePrefix("fc00::/7"),
					},
				},
			},
			{
				name: "MP_UNREACH_NLRI IPv6 end-of-rib",
				toDecode: []byte{
					0x00, 0x00, // withdrawn routes length
					0x00, 0x06, // total path attribute length
					0x80, 0x0f, 0x03, 0x00, 0x02, 0x01, // optional MP_UNREACH_NLRI len 3
				},
				want: &updateMessageForTests{},
			},
			{
				name: "MP_REACH_NLRI IPv6 prefix",
				toDecode: []byte{
					0x00, 0x00, // withdrawn routes length
					0x00, 0x3f, // total path attribute length
					// extended len MP_REACH_NLRI 2001:db8::/64 nhs 2001:db8::2 & fe80::42:c0ff:fe00:202
					0x90, 0x0e, 0x00, 0x2e, 0x00, 0x02, 0x01, 0x20, 0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xfe, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x42, 0xc0, 0xff, 0xfe, 0x00, 0x02, 0x02, 0x00, 0x40, 0x20, 0x01, 0x0d, 0xb8, 0x00, 0x00, 0x00, 0x00,
					0x40, 0x01, 0x01, 0x00, // origin igp
					0x40, 0x02, 0x06, 0x02, 0x01, 0x00, 0x00, 0xfd, 0xea, // as_path 65002
				},
				want: &updateMessageForTests{
					asPath: []uint32{65002},
					ipv6NLRI: []netip.Prefix{
						netip.MustParsePrefix("2001:db8::/64"),
					},
					ipv6NextHops: []netip.Addr{
						netip.MustParseAddr("2001:db8::2"),
						netip.MustParseAddr("fe80::42:c0ff:fe00:202"),
					},
				},
			},
		}
		for _, tt := range cases {
			t.Run(tt.name, func(t *testing.T) {
				m := &updateMessageForTests{}
				n := ud.Decode(m, tt.toDecode)
				if n != nil {
					t.Fatalf("error decoding: %v", n)
				}
				if !reflect.DeepEqual(tt.want, m) {
					t.Fatalf("want: %+v != got: %+v", tt.want, m)
				}
			})
		}
	})

	t.Run("invalid data", func(t *testing.T) {
		cases := []struct {
			name             string
			toDecode         []byte
			wantNotification bool
			wantAsWithdraw   bool
			wantAttrDiscard  bool
		}{
			{
				name: "less than 4 bytes",
				toDecode: []byte{
					0x00, 0x03, // withdrawn routes length
				},
				wantNotification: true,
			},
			{
				name: "missing origin",
				toDecode: []byte{
					0x00, 0x03, // withdrawn routes length
					0x10, 0x0a, 0x00, // withdrawn 10.0.0.0/16
					0x00, 0x10, // total path attribute length
					0x40, 0x02, 0x06, 0x02, 0x01, 0x00, 0x00, 0xfd, 0xea, // as_path 65002
					0x40, 0x03, 0x04, 0xc0, 0x00, 0x02, 0x02, // next_hop 192.0.2.2
					0x08, 0x0a, // nlri 10.0.0.0/8
				},
				wantAsWithdraw: true,
			},
			{
				name: "nonzero atomic aggregate",
				toDecode: []byte{
					0x00, 0x03, // withdrawn routes length
					0x10, 0x0a, 0x00, // withdrawn 10.0.0.0/16
					0x00, 0x18, // total path attribute length
					0x40, 0x01, 0x01, 0x01, // origin egp
					0x40, 0x02, 0x06, 0x02, 0x01, 0x00, 0x00, 0xfd, 0xea, // as_path 65002
					0x40, 0x03, 0x04, 0xc0, 0x00, 0x02, 0x02, // next_hop 192.0.2.2
					0xc0, 0x06, 0x01, 0x01, // invalid atomic aggregate
					0x08, 0x0a, // nlri 10.0.0.0/8
				},
				wantAttrDiscard: true,
			},
		}
		for _, tt := range cases {
			t.Run(tt.name, func(t *testing.T) {
				m := &updateMessageForTests{}
				n := ud.Decode(m, tt.toDecode)
				if n == nil {
					t.Fatal("Decode() returned nil")
				}
				var (
					notif       *Notification
					asWithdraw  *TreatAsWithdrawUpdateErr
					attrDiscard *AttrDiscardUpdateErr
				)
				if tt.wantNotification && !errors.As(n, &notif) {
					t.Error("wanted notification error, none found")
				}
				if tt.wantAsWithdraw && !errors.As(n, &asWithdraw) {
					t.Error("wanted treat as withdraw error, none found")
				}
				if tt.wantAttrDiscard && !errors.As(n, &attrDiscard) {
					t.Error("wanted attr discard error, none found")
				}
			})
		}
	})
}

type fakeUpdateError struct {
	*Notification
}

func (f fakeUpdateError) AsSessionReset() *Notification {
	return f.Notification
}

func TestUpdateNotificationFromErr(t *testing.T) {
	type args struct {
		err error
	}
	tests := []struct {
		name string
		args args
		want *Notification
	}{
		{
			name: "notification",
			args: args{
				err: errors.Join(
					&Notification{Code: 1},
					&Notification{Code: 2},
				),
			},
			want: &Notification{Code: 1},
		},
		{
			name: "treat as withdraw",
			args: args{
				err: errors.Join(
					&TreatAsWithdrawUpdateErr{Notification: &Notification{Code: 1}},
					&AttrDiscardUpdateErr{Notification: &Notification{Code: 2}},
				),
			},
			want: &Notification{Code: 1},
		},
		{
			name: "attr discard",
			args: args{
				err: errors.Join(
					&AttrDiscardUpdateErr{Notification: &Notification{Code: 1}},
					&fakeUpdateError{Notification: &Notification{Code: 2}},
				),
			},
			want: &Notification{Code: 1},
		},
		{
			name: "update error",
			args: args{
				err: errors.Join(
					errors.New("not a notification"),
					&fakeUpdateError{Notification: &Notification{Code: 1}},
				),
			},
			want: &Notification{Code: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, UpdateNotificationFromErr(tt.args.err), "UpdateNotificationFromErr(%v)", tt.args.err)
		})
	}
}
