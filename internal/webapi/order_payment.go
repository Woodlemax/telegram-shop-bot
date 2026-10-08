package webapi

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"

	"shop_bot/internal/storage"
)

func (s *Server) orderPaymentMethods(order *storage.Order) []string {
	methods := make([]string, 0, 6)
	if order.Status != storage.OrderStatusPending || order.PaymentState != storage.PaymentStatePending {
		return methods
	}
	sub := order.SubscriptionProductID > 0 || order.SubscriptionPeriodDays > 0
	if !sub && order.TotalRUB == 0 && order.TotalUSD == 0 && order.TotalStars == 0 && order.TotalTonNano == 0 {
		if _, ok := s.deps.Orders.(interface {
			ConfirmFreeOrder(context.Context, int64, int64) error
		}); ok {
			return append(methods, storage.PaymentMethodFree)
		}
		return methods
	}
	if s.deps.Tg != nil && order.TotalStars > 0 {
		methods = append(methods, storage.PaymentMethodStars)
	}
	if sub || s.deps.StarsOnlyPayments {
		return methods
	}
	if s.deps.Crypto != nil && s.deps.Crypto.Configured() && math.Round(order.TotalUSD*100) > 0 {
		methods = append(methods, storage.PaymentMethodCrypto)
	}
	if s.deps.YooKassa != nil && s.deps.YooKassa.Configured() && math.Round(order.TotalRUB*100) > 0 {
		methods = append(methods, storage.PaymentMethodYooKassa)
	}
	if s.deps.Stripe != nil && s.deps.Stripe.Configured() && math.Round(order.TotalUSD*100) >= 50 {
		methods = append(methods, storage.PaymentMethodStripe)
	}
	if s.deps.TON != nil && s.deps.TON.Configured() && order.TotalTonNano > 0 {
		methods = append(methods, storage.PaymentMethodTON)
	}
	if s.deps.Nowpayments != nil && s.deps.Nowpayments.Configured() && math.Round(order.TotalUSD*100) > 0 {
		methods = append(methods, storage.PaymentMethodNowpayments)
	}
	return methods
}

func (s *Server) handleOrderPay(w http.ResponseWriter, r *http.Request, auth *AuthResult) {
	w.Header().Set("Cache-Control", "no-store")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.writeError(w, http.StatusBadRequest, "webapp_err_bad_request")
		return
	}
	var req struct {
		Method string `json:"method"`
	}
	if !s.decodeBody(w, r, &req) {
		return
	}
	order, err := s.deps.Orders.GetOrder(r.Context(), id)
	if errors.Is(err, storage.ErrNotFound) || (err == nil && (order == nil || order.UserID != auth.User.ID)) {
		s.writeError(w, http.StatusNotFound, "webapp_err_not_found")
		return
	}
	if err != nil {
		s.logger.Error("webapi: load order for resumed payment", "order_id", id)
		s.writeError(w, http.StatusInternalServerError, "webapp_err_internal")
		return
	}
	if order.Status != storage.OrderStatusPending || order.PaymentState != storage.PaymentStatePending {
		s.writeError(w, http.StatusConflict, "webapp_order_pay_unavailable")
		return
	}
	allowed := false
	for _, method := range s.orderPaymentMethods(order) {
		if req.Method == method {
			allowed = true
			break
		}
	}
	if !allowed {
		s.writeError(w, http.StatusBadRequest, "webapp_order_pay_method_unavailable")
		return
	}
	subPeriod := 0
	if order.SubscriptionProductID > 0 || order.SubscriptionPeriodDays > 0 {
		subPeriod = subscriptionPeriodSeconds
	}
	s.issueOrderPayment(w, r, auth, order, req.Method, subPeriod)
}
